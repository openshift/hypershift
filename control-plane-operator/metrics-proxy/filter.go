package metricsproxy

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
)

const (
	relabelActionKeep = "keep"
	relabelActionDrop = "drop"

	// Prometheus relabel defaults for fields left unset in a RelabelConfig.
	defaultRelabelAction    = "replace"
	defaultRelabelRegex     = "(.*)"
	defaultRelabelSeparator = ";"
)

// Filter applies the compiled metric relabel rules of a component to the
// metric families scraped from it.
type Filter struct {
	log logr.Logger
}

// NewFilter returns a Filter that logs through the given logger.
func NewFilter(log logr.Logger) *Filter {
	return &Filter{log: log}
}

// Apply returns the metric families the component's sample filter permits.
// Components without a sample filter (the All metrics set and PodMonitor-based
// components) forward every family unchanged.
func (f *Filter) Apply(componentName string, cfg ComponentConfig, families map[string]*dto.MetricFamily) map[string]*dto.MetricFamily {
	if cfg.sampleFilter == nil {
		return families
	}
	return cfg.sampleFilter.apply(f.log.WithValues("component", componentName), families)
}

// sampleFilter holds the compiled keep and drop rules of one component and
// evaluates them against every exposed sample. It is immutable after
// construction, apart from the atomic log-once flag, so a single instance is
// safe for concurrent use by all requests.
type sampleFilter struct {
	// forwardNothing drops every sample. It is set when the component has no
	// relabel rules at all, or when a keep or drop rule cannot be compiled
	// (fail closed).
	forwardNothing bool
	// rules are the supported keep and drop rules in configured order.
	// Unsupported actions are ignored and not part of it, so a configuration
	// consisting only of unsupported actions filters nothing.
	rules []relabelRule

	collisionLogged atomic.Bool
}

// relabelRule is a compiled keep or drop RelabelConfig.
type relabelRule struct {
	keep         bool
	sourceLabels []string
	separator    string
	regex        *regexp.Regexp
}

// newSampleFilter compiles the relabel rules of a component. It returns nil
// when the component forwards all samples unchanged. Unsupported actions are
// logged once per component and action and are ignored. An unknown filter
// mode or an invalid keep or drop regex makes the component forward nothing.
func newSampleFilter(log logr.Logger, componentName string, mode MetricsFilterMode, configs []prometheusoperatorv1.RelabelConfig) *sampleFilter {
	switch mode {
	case "", MetricsFilterNone:
		if len(configs) > 0 {
			log.Info("Ignoring metric relabel configs of a component whose metrics filter forwards all samples",
				"component", componentName, "metricsFilter", mode)
		}
		return nil
	case MetricsFilterRelabel:
	default:
		log.Error(fmt.Errorf("unknown metrics filter %q", mode), "Forwarding no samples for component", "component", componentName)
		return &sampleFilter{forwardNothing: true}
	}

	if len(configs) == 0 {
		return &sampleFilter{forwardNothing: true}
	}

	filter := &sampleFilter{}
	loggedActions := make(map[string]bool)
	for i, rc := range configs {
		action := strings.ToLower(rc.Action)
		if action == "" {
			action = defaultRelabelAction
		}
		if action != relabelActionKeep && action != relabelActionDrop {
			if !loggedActions[action] {
				loggedActions[action] = true
				log.Info("Ignoring unsupported metric relabel action, only keep and drop are applied",
					"component", componentName, "action", action)
			}
			continue
		}

		regexString := rc.Regex
		if regexString == "" {
			regexString = defaultRelabelRegex
		}
		regex, err := compileRelabelRegex(regexString)
		if err != nil {
			log.Error(err, "Invalid metric relabel regex, forwarding no samples for component",
				"component", componentName, "rule", i, "action", action)
			return &sampleFilter{forwardNothing: true}
		}

		separator := defaultRelabelSeparator
		if rc.Separator != nil {
			separator = *rc.Separator
		}
		sourceLabels := make([]string, 0, len(rc.SourceLabels))
		for _, label := range rc.SourceLabels {
			sourceLabels = append(sourceLabels, string(label))
		}

		filter.rules = append(filter.rules, relabelRule{
			keep:         action == relabelActionKeep,
			sourceLabels: sourceLabels,
			separator:    separator,
			regex:        regex,
		})
	}
	return filter
}

// compileRelabelRegex compiles a relabel regex anchored the same way
// Prometheus relabeling does. The expression is validated on its own first:
// anchoring can turn an invalid expression such as "x)|(.*" into a valid one
// with a different meaning, which must not widen what a rule matches.
func compileRelabelRegex(expression string) (*regexp.Regexp, error) {
	if _, err := regexp.Compile(expression); err != nil {
		return nil, err
	}
	return regexp.Compile("^(?s:" + expression + ")$")
}

func (f *sampleFilter) apply(log logr.Logger, families map[string]*dto.MetricFamily) map[string]*dto.MetricFamily {
	if f.forwardNothing {
		return map[string]*dto.MetricFamily{}
	}
	if len(f.rules) == 0 {
		return families
	}

	result := make(map[string]*dto.MetricFamily, len(families))
	derived := make(map[string]*derivedFamily)
	for key, mf := range families {
		switch mf.GetType() {
		case dto.MetricType_SUMMARY, dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
			if kept := f.filterCompositeFamily(key, mf, derived); kept != nil {
				result[key] = kept
			}
		default:
			if kept := f.filterSimpleFamily(mf); kept != nil {
				result[key] = kept
			}
		}
	}

	// result now holds every family that survived filtering unchanged or as a
	// subset, so derived names are checked against surviving families only:
	// a family that filtering removed entirely must not suppress permitted
	// samples. A partially kept source family is never in result.
	var collisions []string
	for name, d := range derived {
		if _, clashes := result[name]; d.collided || clashes {
			collisions = append(collisions, name)
			continue
		}
		result[name] = d.family
	}
	if len(collisions) > 0 && f.collisionLogged.CompareAndSwap(false, true) {
		sort.Strings(collisions)
		log.Info("Dropping filtered histogram or summary samples whose metric name collides with another metric family",
			"metrics", collisions)
	}
	return result
}

// filterSimpleFamily filters a counter, gauge or untyped family, where every
// metric exposes exactly one sample named after the family.
func (f *sampleFilter) filterSimpleFamily(mf *dto.MetricFamily) *dto.MetricFamily {
	name := mf.GetName()
	kept := make([]*dto.Metric, 0, len(mf.Metric))
	for _, m := range mf.Metric {
		if f.keepSample(name, m.Label, "", "") {
			kept = append(kept, m)
		}
	}
	return familyWithMetrics(mf, kept)
}

// filterCompositeFamily filters a summary or histogram family. Metrics whose
// exposed samples are all kept or all dropped keep the family's type. When any
// metric keeps only part of its samples, the text encoder could no longer
// render the typed family without re-adding dropped samples (_sum, _count and
// the +Inf bucket), so the kept samples of the whole family are emitted as
// untyped families into derived instead, and nil is returned.
func (f *sampleFilter) filterCompositeFamily(sourceKey string, mf *dto.MetricFamily, derived map[string]*derivedFamily) *dto.MetricFamily {
	name := mf.GetName()
	perMetric := make([][]exposedSample, len(mf.Metric))
	keptPerMetric := make([][]bool, len(mf.Metric))
	fullyKept := make([]*dto.Metric, 0, len(mf.Metric))
	partial := false

	for i, m := range mf.Metric {
		samples := exposedSamples(mf.GetType(), m)
		keep := make([]bool, len(samples))
		keptCount := 0
		for j, s := range samples {
			if f.keepSample(name+s.suffix, m.Label, s.extraLabel, s.extraValue) {
				keep[j] = true
				keptCount++
			}
		}
		perMetric[i] = samples
		keptPerMetric[i] = keep
		switch {
		case keptCount == 0:
		case keptCount == len(samples):
			fullyKept = append(fullyKept, m)
		default:
			partial = true
		}
	}

	if !partial {
		return familyWithMetrics(mf, fullyKept)
	}

	for i, m := range mf.Metric {
		for j, s := range perMetric[i] {
			if !keptPerMetric[i][j] || !s.inSource {
				continue
			}
			addDerivedSample(derived, sourceKey, name+s.suffix, mf.Help, untypedSample(m, s))
		}
	}
	return nil
}

func (f *sampleFilter) keepSample(name string, labels []*dto.LabelPair, extraLabel, extraValue string) bool {
	for i := range f.rules {
		rule := &f.rules[i]
		matched := rule.regex.MatchString(rule.sourceValue(name, labels, extraLabel, extraValue))
		// keep drops non-matching samples, drop drops matching samples.
		if matched != rule.keep {
			return false
		}
	}
	return true
}

// sourceValue joins the values of the rule's source labels with its separator.
// Missing labels contribute an empty string, as in Prometheus.
func (r *relabelRule) sourceValue(name string, labels []*dto.LabelPair, extraLabel, extraValue string) string {
	if len(r.sourceLabels) == 1 {
		// Avoid allocating for the common single source label case.
		return sampleLabelValue(r.sourceLabels[0], name, labels, extraLabel, extraValue)
	}
	values := make([]string, len(r.sourceLabels))
	for i, label := range r.sourceLabels {
		values[i] = sampleLabelValue(label, name, labels, extraLabel, extraValue)
	}
	return strings.Join(values, r.separator)
}

func sampleLabelValue(label, name string, labels []*dto.LabelPair, extraLabel, extraValue string) string {
	if label == model.MetricNameLabel {
		return name
	}
	if extraLabel != "" && label == extraLabel {
		return extraValue
	}
	for _, lp := range labels {
		if lp.GetName() == label {
			return lp.GetValue()
		}
	}
	return ""
}

// exposedSample is one sample of a summary or histogram metric as the text
// encoder renders it.
type exposedSample struct {
	suffix     string
	extraLabel string
	extraValue string
	value      float64
	// inSource is false for samples the encoder synthesizes from data missing
	// in the scraped metric: _sum or _count, or a +Inf bucket without a count.
	inSource bool
}

// exposedSamples lists the samples the text encoder renders for a summary or
// histogram metric, mirroring github.com/prometheus/common/expfmt.
func exposedSamples(metricType dto.MetricType, m *dto.Metric) []exposedSample {
	switch metricType {
	case dto.MetricType_SUMMARY:
		s := m.GetSummary()
		if s == nil {
			return nil
		}
		samples := make([]exposedSample, 0, len(s.Quantile)+2)
		for _, q := range s.Quantile {
			samples = append(samples, exposedSample{
				extraLabel: model.QuantileLabel,
				extraValue: formatLabelFloat(q.GetQuantile()),
				value:      q.GetValue(),
				inSource:   true,
			})
		}
		return append(samples,
			exposedSample{suffix: "_sum", value: s.GetSampleSum(), inSource: s.SampleSum != nil},
			exposedSample{suffix: "_count", value: float64(s.GetSampleCount()), inSource: s.SampleCount != nil},
		)
	case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
		h := m.GetHistogram()
		if h == nil {
			return nil
		}
		count := h.GetSampleCountFloat()
		if count == 0 {
			count = float64(h.GetSampleCount())
		}
		samples := make([]exposedSample, 0, len(h.Bucket)+3)
		infSeen := false
		for _, b := range h.Bucket {
			value := b.GetCumulativeCountFloat()
			if value == 0 {
				value = float64(b.GetCumulativeCount())
			}
			samples = append(samples, exposedSample{
				suffix:     "_bucket",
				extraLabel: model.BucketLabel,
				extraValue: formatLabelFloat(b.GetUpperBound()),
				value:      value,
				inSource:   true,
			})
			if math.IsInf(b.GetUpperBound(), +1) {
				infSeen = true
			}
		}
		if !infSeen {
			// The encoder renders the +Inf bucket from the sample count, so
			// it is source data whenever the count is.
			samples = append(samples, exposedSample{
				suffix:     "_bucket",
				extraLabel: model.BucketLabel,
				extraValue: "+Inf",
				value:      count,
				inSource:   h.SampleCount != nil || h.SampleCountFloat != nil,
			})
		}
		return append(samples,
			exposedSample{suffix: "_sum", value: h.GetSampleSum(), inSource: h.SampleSum != nil},
			exposedSample{suffix: "_count", value: count, inSource: h.SampleCount != nil || h.SampleCountFloat != nil},
		)
	default:
		return nil
	}
}

// formatLabelFloat formats le and quantile values like the text encoder does.
func formatLabelFloat(v float64) string {
	if v == 0 {
		// The encoder writes "0" for both zero and negative zero.
		return "0"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// familyWithMetrics returns mf when every metric is kept, nil when none is
// kept, and otherwise a copy of mf restricted to the kept metrics.
func familyWithMetrics(mf *dto.MetricFamily, kept []*dto.Metric) *dto.MetricFamily {
	switch len(kept) {
	case 0:
		return nil
	case len(mf.Metric):
		return mf
	}
	return &dto.MetricFamily{
		Name:   mf.Name,
		Help:   mf.Help,
		Type:   mf.Type,
		Unit:   mf.Unit,
		Metric: kept,
	}
}

// derivedFamily is an untyped family holding the kept samples of a partially
// filtered summary or histogram.
type derivedFamily struct {
	family *dto.MetricFamily
	// source is the key of the family the samples were derived from.
	source string
	// collided is set when samples from more than one source family map to
	// the same name; such samples are dropped regardless of map iteration order.
	collided bool
}

func addDerivedSample(derived map[string]*derivedFamily, source, name string, help *string, metric *dto.Metric) {
	d, ok := derived[name]
	if !ok {
		d = &derivedFamily{
			family: &dto.MetricFamily{
				Name: ptr.To(name),
				Help: help,
				Type: dto.MetricType_UNTYPED.Enum(),
			},
			source: source,
		}
		derived[name] = d
	} else if d.source != source {
		d.collided = true
	}
	d.family.Metric = append(d.family.Metric, metric)
}

// untypedSample converts one sample of a summary or histogram metric into an
// untyped metric with its own copy of the labels, the le or quantile label
// last (as the text encoder renders it), and the original timestamp.
func untypedSample(m *dto.Metric, s exposedSample) *dto.Metric {
	labels := slices.Clone(m.Label)
	if s.extraLabel != "" {
		labels = append(labels, &dto.LabelPair{Name: ptr.To(s.extraLabel), Value: ptr.To(s.extraValue)})
	}
	return &dto.Metric{
		Label:       labels,
		Untyped:     &dto.Untyped{Value: ptr.To(s.value)},
		TimestampMs: m.TimestampMs,
	}
}
