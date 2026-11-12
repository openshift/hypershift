package metrics

import (
	"fmt"
	"maps"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
)

// labelNames returns the Prometheus label-name slice for a set of Labels.
func labelNames(labels []Label) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names
}

// mustBeExhaustive panics if values gives an Exhaustive dimension a value outside its
// Values, as the client panics on inconsistent label names. DeletePartialMatch only
// deletes listed values for an omitted Exhaustive dimension, so an unlisted value
// would leave a series that is never deleted.
func mustBeExhaustive(labels []Label, values map[string]string) {
	for _, l := range labels {
		if !l.Exhaustive {
			continue
		}
		if v, ok := values[l.Name]; ok && !slices.ContainsFunc(l.Values, func(lv Value) bool { return lv.Name == v }) {
			panic(fmt.Sprintf("label %q is Exhaustive but value %q is not in its Values", l.Name, v))
		}
	}
}

// exactLabelSets expands a partial label set into every full label set it can match,
// filling each omitted dimension with its Values. Deleting those by exact labels avoids
// the scan over every series that the client's DeletePartialMatch performs. ok is false
// when an omitted dimension isn't Exhaustive or partial names a dimension the metric
// doesn't have.
func exactLabelSets(labels []Label, partial map[string]string) (sets []map[string]string, ok bool) {
	sets = []map[string]string{{}}
	matched := 0
	for _, l := range labels {
		if v, has := partial[l.Name]; has {
			matched++
			for _, s := range sets {
				s[l.Name] = v
			}
			continue
		}
		if !l.Exhaustive {
			return nil, false
		}
		expanded := make([]map[string]string, 0, len(sets)*len(l.Values))
		for _, s := range sets {
			for _, v := range l.Values {
				e := maps.Clone(s)
				e[l.Name] = v.Name
				expanded = append(expanded, e)
			}
		}
		sets = expanded
	}
	return sets, matched == len(partial)
}

type PrometheusCounter struct {
	*prometheus.CounterVec
	labels []Label
	stage  Stage
}

func NewPrometheusCounter(registry prometheus.Registerer, opts prometheus.CounterOpts, labels []Label, stage Stage) CounterMetric {
	c := prometheus.NewCounterVec(opts, labelNames(labels))
	registry.MustRegister(c)
	return &PrometheusCounter{CounterVec: c, labels: labels, stage: stage}
}

func (pc *PrometheusCounter) Labels() []Label { return pc.labels }

func (pc *PrometheusCounter) Stage() Stage { return pc.stage }

func (pc *PrometheusCounter) Inc(labels map[string]string) {
	mustBeExhaustive(pc.labels, labels)
	pc.CounterVec.With(labels).Inc()
}

func (pc *PrometheusCounter) Add(v float64, labels map[string]string) {
	mustBeExhaustive(pc.labels, labels)
	pc.CounterVec.With(labels).Add(v)
}

func (pc *PrometheusCounter) Delete(labels map[string]string) {
	pc.CounterVec.Delete(labels)
}

func (pc *PrometheusCounter) DeletePartialMatch(labels map[string]string) {
	if sets, ok := exactLabelSets(pc.labels, labels); ok {
		for _, s := range sets {
			pc.CounterVec.Delete(s)
		}
		return
	}
	pc.CounterVec.DeletePartialMatch(labels)
}

func (pc *PrometheusCounter) Reset() {
	pc.CounterVec.Reset()
}

type PrometheusGauge struct {
	*prometheus.GaugeVec
	labels []Label
	stage  Stage
}

func NewPrometheusGauge(registry prometheus.Registerer, opts prometheus.GaugeOpts, labels []Label, stage Stage) GaugeMetric {
	g := prometheus.NewGaugeVec(opts, labelNames(labels))
	registry.MustRegister(g)
	return &PrometheusGauge{GaugeVec: g, labels: labels, stage: stage}
}

func (pg *PrometheusGauge) Labels() []Label { return pg.labels }

func (pg *PrometheusGauge) Stage() Stage { return pg.stage }

func (pg *PrometheusGauge) Set(v float64, labels map[string]string) {
	mustBeExhaustive(pg.labels, labels)
	pg.GaugeVec.With(labels).Set(v)
}

func (pg *PrometheusGauge) Delete(labels map[string]string) {
	pg.GaugeVec.Delete(labels)
}

func (pg *PrometheusGauge) DeletePartialMatch(labels map[string]string) {
	if sets, ok := exactLabelSets(pg.labels, labels); ok {
		for _, s := range sets {
			pg.GaugeVec.Delete(s)
		}
		return
	}
	pg.GaugeVec.DeletePartialMatch(labels)
}

func (pg *PrometheusGauge) Reset() {
	pg.GaugeVec.Reset()
}

type PrometheusHistogram struct {
	*prometheus.HistogramVec
	labels []Label
	stage  Stage
}

func NewPrometheusHistogram(registry prometheus.Registerer, opts prometheus.HistogramOpts, labels []Label, stage Stage) ObservationMetric {
	h := prometheus.NewHistogramVec(opts, labelNames(labels))
	registry.MustRegister(h)
	return &PrometheusHistogram{HistogramVec: h, labels: labels, stage: stage}
}

func (ph *PrometheusHistogram) Labels() []Label { return ph.labels }

func (ph *PrometheusHistogram) Stage() Stage { return ph.stage }

func (ph *PrometheusHistogram) Observe(v float64, labels map[string]string) {
	mustBeExhaustive(ph.labels, labels)
	ph.HistogramVec.With(labels).Observe(v)
}

func (ph *PrometheusHistogram) Delete(labels map[string]string) {
	ph.HistogramVec.Delete(labels)
}

func (ph *PrometheusHistogram) DeletePartialMatch(labels map[string]string) {
	if sets, ok := exactLabelSets(ph.labels, labels); ok {
		for _, s := range sets {
			ph.HistogramVec.Delete(s)
		}
		return
	}
	ph.HistogramVec.DeletePartialMatch(labels)
}

func (ph *PrometheusHistogram) Reset() {
	ph.HistogramVec.Reset()
}

type PrometheusSummary struct {
	*prometheus.SummaryVec
	labels []Label
	stage  Stage
}

func NewPrometheusSummary(registry prometheus.Registerer, opts prometheus.SummaryOpts, labels []Label, stage Stage) ObservationMetric {
	s := prometheus.NewSummaryVec(opts, labelNames(labels))
	registry.MustRegister(s)
	return &PrometheusSummary{SummaryVec: s, labels: labels, stage: stage}
}

func (ps *PrometheusSummary) Labels() []Label { return ps.labels }

func (ps *PrometheusSummary) Stage() Stage { return ps.stage }

func (ps *PrometheusSummary) Observe(v float64, labels map[string]string) {
	mustBeExhaustive(ps.labels, labels)
	ps.SummaryVec.With(labels).Observe(v)
}

func (ps *PrometheusSummary) Delete(labels map[string]string) {
	ps.SummaryVec.Delete(labels)
}

func (ps *PrometheusSummary) DeletePartialMatch(labels map[string]string) {
	if sets, ok := exactLabelSets(ps.labels, labels); ok {
		for _, s := range sets {
			ps.SummaryVec.Delete(s)
		}
		return
	}
	ps.SummaryVec.DeletePartialMatch(labels)
}

func (ps *PrometheusSummary) Reset() {
	ps.SummaryVec.Reset()
}
