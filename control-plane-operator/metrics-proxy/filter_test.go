package metricsproxy

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// testExposition is a scrape of a control plane component with a counter, a
// gauge, a histogram and a summary.
const testExposition = `# HELP apiserver_request_total Counter of apiserver requests.
# TYPE apiserver_request_total counter
apiserver_request_total{code="200",verb="GET"} 10
apiserver_request_total{code="500",verb="GET"} 2
# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 1.2e+08
# HELP apiserver_request_duration_seconds Response latency.
# TYPE apiserver_request_duration_seconds histogram
apiserver_request_duration_seconds_bucket{verb="GET",le="0.1"} 5
apiserver_request_duration_seconds_bucket{verb="GET",le="1"} 9
apiserver_request_duration_seconds_bucket{verb="GET",le="+Inf"} 10
apiserver_request_duration_seconds_sum{verb="GET"} 3.5
apiserver_request_duration_seconds_count{verb="GET"} 10
# HELP rpc_latency_seconds RPC latency.
# TYPE rpc_latency_seconds summary
rpc_latency_seconds{quantile="0.5"} 0.2
rpc_latency_seconds{quantile="0.99"} 0.9
rpc_latency_seconds_sum 12
rpc_latency_seconds_count 40
`

var (
	testCounterLines = []string{
		"# TYPE apiserver_request_total counter",
		`apiserver_request_total{code="200",verb="GET"} 10`,
		`apiserver_request_total{code="500",verb="GET"} 2`,
	}
	testGaugeLines = []string{
		"# TYPE process_resident_memory_bytes gauge",
		"process_resident_memory_bytes 1.2e+08",
	}
	testHistogramLines = []string{
		"# TYPE apiserver_request_duration_seconds histogram",
		`apiserver_request_duration_seconds_bucket{verb="GET",le="0.1"} 5`,
		`apiserver_request_duration_seconds_bucket{verb="GET",le="1"} 9`,
		`apiserver_request_duration_seconds_bucket{verb="GET",le="+Inf"} 10`,
		`apiserver_request_duration_seconds_sum{verb="GET"} 3.5`,
		`apiserver_request_duration_seconds_count{verb="GET"} 10`,
	}
	testSummaryLines = []string{
		"# TYPE rpc_latency_seconds summary",
		`rpc_latency_seconds{quantile="0.5"} 0.2`,
		`rpc_latency_seconds{quantile="0.99"} 0.9`,
		"rpc_latency_seconds_sum 12",
		"rpc_latency_seconds_count 40",
	}
)

func TestFilter_Apply(t *testing.T) {
	t.Parallel()

	keepName := func(regex string) prometheusoperatorv1.RelabelConfig {
		return prometheusoperatorv1.RelabelConfig{Action: "keep", Regex: regex, SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}}
	}
	dropName := func(regex string) prometheusoperatorv1.RelabelConfig {
		return prometheusoperatorv1.RelabelConfig{Action: "drop", Regex: regex, SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}}
	}

	tests := []struct {
		name      string
		mode      MetricsFilterMode
		rules     []prometheusoperatorv1.RelabelConfig
		families  func(t *testing.T) map[string]*dto.MetricFamily
		wantLines []string
	}{
		{
			name:      "When the metrics filter is None, it should forward every family unchanged",
			mode:      MetricsFilterNone,
			wantLines: concatLines(testHistogramLines, testCounterLines, testGaugeLines, testSummaryLines),
		},
		{
			name:      "When the metrics filter is unset, it should forward every family unchanged",
			mode:      "",
			wantLines: concatLines(testHistogramLines, testCounterLines, testGaugeLines, testSummaryLines),
		},
		{
			name:      "When the metrics filter is Relabel without rules, it should forward no samples",
			mode:      MetricsFilterRelabel,
			wantLines: nil,
		},
		{
			name:      "When a keep rule matches a counter name, it should forward only that typed family",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{keepName("apiserver_request_total")},
			wantLines: testCounterLines,
		},
		{
			name: "When a keep rule is followed by a drop rule on multiple labels, it should apply both in order",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("apiserver_request_.*"),
				{Action: "drop", Regex: "apiserver_request_total;5..", SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "code"}},
			},
			wantLines: concatLines(
				testHistogramLines,
				[]string{"# TYPE apiserver_request_total counter", `apiserver_request_total{code="200",verb="GET"} 10`},
			),
		},
		{
			name:      "When a drop rule matches a gauge name, it should forward every other family unchanged",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{dropName("process_.*")},
			wantLines: concatLines(testHistogramLines, testCounterLines, testSummaryLines),
		},
		{
			name:      "When a drop rule uses the .* regex on the metric name, it should forward no samples",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{dropName(".*")},
			wantLines: nil,
		},
		{
			name:      "When a drop rule uses the (.*) regex on the metric name, it should forward no samples",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{dropName("(.*)")},
			wantLines: nil,
		},
		{
			name:  "When a keep rule selects histogram buckets by name, it should forward only the buckets as untyped samples",
			mode:  MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{keepName("apiserver_request_duration_seconds_bucket")},
			wantLines: []string{
				"# TYPE apiserver_request_duration_seconds_bucket untyped",
				`apiserver_request_duration_seconds_bucket{verb="GET",le="0.1"} 5`,
				`apiserver_request_duration_seconds_bucket{verb="GET",le="1"} 9`,
				`apiserver_request_duration_seconds_bucket{verb="GET",le="+Inf"} 10`,
			},
		},
		{
			name: "When a keep rule selects histogram buckets by name and le, it should forward only the matching buckets",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        `apiserver_request_duration_seconds_bucket;(0\.1|\+Inf)`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			}},
			wantLines: []string{
				"# TYPE apiserver_request_duration_seconds_bucket untyped",
				`apiserver_request_duration_seconds_bucket{verb="GET",le="0.1"} 5`,
				`apiserver_request_duration_seconds_bucket{verb="GET",le="+Inf"} 10`,
			},
		},
		{
			name: "When a keep rule matches every histogram sample, it should keep the histogram type",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("apiserver_request_duration_seconds_(bucket|sum|count)"),
			},
			wantLines: testHistogramLines,
		},
		{
			name: "When a keep rule selects a summary quantile, it should forward only that quantile as an untyped sample",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        `rpc_latency_seconds;0\.99`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "quantile"},
			}},
			wantLines: []string{
				"# TYPE rpc_latency_seconds untyped",
				`rpc_latency_seconds{quantile="0.99"} 0.9`,
			},
		},
		{
			name:  "When a drop rule removes a summary count, it should forward the remaining summary samples untyped without a count",
			mode:  MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{dropName("rpc_latency_seconds_count")},
			wantLines: concatLines(testHistogramLines, testCounterLines, testGaugeLines, []string{
				"# TYPE rpc_latency_seconds untyped",
				`rpc_latency_seconds{quantile="0.5"} 0.2`,
				`rpc_latency_seconds{quantile="0.99"} 0.9`,
				"# TYPE rpc_latency_seconds_sum untyped",
				"rpc_latency_seconds_sum 12",
			}),
		},
		{
			name: "When source labels use a custom separator, it should join the values with it",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "GET/200",
				Separator:    ptr.To("/"),
				SourceLabels: []prometheusoperatorv1.LabelName{"verb", "code"},
			}},
			wantLines: []string{"# TYPE apiserver_request_total counter", `apiserver_request_total{code="200",verb="GET"} 10`},
		},
		{
			name: "When a source label is missing, it should match it as an empty value",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "|200",
				SourceLabels: []prometheusoperatorv1.LabelName{"code"},
			}},
			wantLines: concatLines(
				testHistogramLines,
				[]string{"# TYPE apiserver_request_total counter", `apiserver_request_total{code="200",verb="GET"} 10`},
				testGaugeLines, testSummaryLines,
			),
		},
		{
			name: "When a keep rule has no regex, it should default to (.*) and keep every sample",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				SourceLabels: []prometheusoperatorv1.LabelName{"code"},
			}},
			wantLines: concatLines(testHistogramLines, testCounterLines, testGaugeLines, testSummaryLines),
		},
		{
			name:      "When a drop rule has neither source labels nor regex, it should match the empty value and drop every sample",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{{Action: "drop"}},
			wantLines: nil,
		},
		{
			name: "When a keep rule partially matches a regex, it should require a fully anchored match",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("apiserver_request"),
			},
			wantLines: nil,
		},
		{
			name: "When a rule references a label injected before filtering, it should match on it",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "clusters-test",
				SourceLabels: []prometheusoperatorv1.LabelName{"namespace"},
			}},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				families := parseFamilies(t, testExposition)
				m := families["process_resident_memory_bytes"].Metric[0]
				m.Label = append(m.Label, &dto.LabelPair{Name: ptr.To("namespace"), Value: ptr.To("clusters-test")})
				return families
			},
			wantLines: []string{
				"# TYPE process_resident_memory_bytes gauge",
				`process_resident_memory_bytes{namespace="clusters-test"} 1.2e+08`,
			},
		},
		{
			name: "When only unsupported actions are configured, it should forward every family unchanged",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "labeldrop", Regex: "code"},
				{TargetLabel: "foo", Replacement: ptr.To("bar")},
			},
			wantLines: concatLines(testHistogramLines, testCounterLines, testGaugeLines, testSummaryLines),
		},
		{
			name: "When unsupported actions are mixed with a keep rule, it should apply only the keep rule",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "labeldrop", Regex: "code"},
				keepName("apiserver_request_total"),
			},
			wantLines: testCounterLines,
		},
		{
			name:      "When a keep regex does not compile, it should fail closed and forward no samples",
			mode:      MetricsFilterRelabel,
			rules:     []prometheusoperatorv1.RelabelConfig{keepName("(apiserver_request_total")},
			wantLines: nil,
		},
		{
			name: "When a drop regex does not compile, it should fail closed and forward no samples",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName(".*"),
				dropName("process_[a-"),
			},
			wantLines: nil,
		},
		{
			name:      "When the metrics filter mode is unknown, it should fail closed and forward no samples",
			mode:      MetricsFilterMode("Unknown"),
			rules:     []prometheusoperatorv1.RelabelConfig{keepName(".*")},
			wantLines: nil,
		},
		{
			name:  "When histogram series are fully kept or fully dropped, it should keep the histogram type for the kept series",
			mode:  MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{Action: "drop", Regex: "LIST", SourceLabels: []prometheusoperatorv1.LabelName{"verb"}}},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return parseFamilies(t, `# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{verb="GET",le="1"} 1
request_duration_seconds_bucket{verb="GET",le="+Inf"} 2
request_duration_seconds_sum{verb="GET"} 1.5
request_duration_seconds_count{verb="GET"} 2
request_duration_seconds_bucket{verb="LIST",le="1"} 3
request_duration_seconds_bucket{verb="LIST",le="+Inf"} 4
request_duration_seconds_sum{verb="LIST"} 2.5
request_duration_seconds_count{verb="LIST"} 4
`)
			},
			wantLines: []string{
				"# TYPE request_duration_seconds histogram",
				`request_duration_seconds_bucket{verb="GET",le="1"} 1`,
				`request_duration_seconds_bucket{verb="GET",le="+Inf"} 2`,
				`request_duration_seconds_sum{verb="GET"} 1.5`,
				`request_duration_seconds_count{verb="GET"} 2`,
			},
		},
		{
			name: "When samples carry timestamps, it should preserve them on flattened samples",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("request_duration_seconds_(bucket|count)"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return parseFamilies(t, `# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{le="1"} 1 1700000000000
request_duration_seconds_bucket{le="+Inf"} 2 1700000000000
request_duration_seconds_sum 1.5 1700000000000
request_duration_seconds_count 2 1700000000000
`)
			},
			wantLines: []string{
				"# TYPE request_duration_seconds_bucket untyped",
				`request_duration_seconds_bucket{le="1"} 1 1700000000000`,
				`request_duration_seconds_bucket{le="+Inf"} 2 1700000000000`,
				"# TYPE request_duration_seconds_count untyped",
				"request_duration_seconds_count 2 1700000000000",
			},
		},
		{
			name: "When a histogram has no +Inf bucket and the rules drop the +Inf sample, it should not synthesize one",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        `.*;\+Inf`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			}},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return map[string]*dto.MetricFamily{"latency_seconds": histogramWithoutInfBucket("latency_seconds")}
			},
			wantLines: []string{
				"# TYPE latency_seconds_bucket untyped",
				`latency_seconds_bucket{le="1"} 3`,
				"# TYPE latency_seconds_count untyped",
				"latency_seconds_count 4",
				"# TYPE latency_seconds_sum untyped",
				"latency_seconds_sum 2",
			},
		},
		{
			name: "When a histogram has no +Inf bucket and the rules keep every exposed sample, it should keep the histogram type",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("latency_seconds_.*"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return map[string]*dto.MetricFamily{"latency_seconds": histogramWithoutInfBucket("latency_seconds")}
			},
			wantLines: []string{
				"# TYPE latency_seconds histogram",
				`latency_seconds_bucket{le="1"} 3`,
				`latency_seconds_bucket{le="+Inf"} 4`,
				"latency_seconds_sum 2",
				"latency_seconds_count 4",
			},
		},
		{
			name: "When a keep regex is invalid on its own but valid once anchored, it should fail closed",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("x)|(.*"),
			},
			wantLines: nil,
		},
		{
			name: "When a histogram has no +Inf bucket and the rules keep only the count-backed +Inf bucket, it should forward it",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        `latency_seconds_bucket;\+Inf`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			}},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return map[string]*dto.MetricFamily{"latency_seconds": histogramWithoutInfBucket("latency_seconds")}
			},
			wantLines: []string{
				"# TYPE latency_seconds_bucket untyped",
				`latency_seconds_bucket{le="+Inf"} 4`,
			},
		},
		{
			name: "When a histogram has neither +Inf bucket nor sum and count, it should not synthesize them when flattening",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("latency_seconds_bucket"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				mf := histogramWithoutInfBucket("latency_seconds")
				mf.Metric[0].Histogram.SampleCount = nil
				mf.Metric[0].Histogram.SampleSum = nil
				return map[string]*dto.MetricFamily{"latency_seconds": mf}
			},
			wantLines: []string{
				"# TYPE latency_seconds_bucket untyped",
				`latency_seconds_bucket{le="1"} 3`,
			},
		},
		{
			name: "When a bucket bound is negative zero, it should match and expose it as le 0 like the encoder",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        `offset_seconds_bucket;0`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			}},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return parseFamilies(t, `# TYPE offset_seconds histogram
offset_seconds_bucket{le="-0"} 1
offset_seconds_bucket{le="+Inf"} 2
offset_seconds_sum 0.5
offset_seconds_count 2
`)
			},
			wantLines: []string{
				"# TYPE offset_seconds_bucket untyped",
				`offset_seconds_bucket{le="0"} 1`,
			},
		},
		{
			name: "When a negative zero bucket is kept with the whole histogram, it should render le 0 through the encoder",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("offset_seconds_.*"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return parseFamilies(t, `# TYPE offset_seconds histogram
offset_seconds_bucket{le="-0"} 1
offset_seconds_bucket{le="+Inf"} 2
offset_seconds_sum 0.5
offset_seconds_count 2
`)
			},
			wantLines: []string{
				"# TYPE offset_seconds histogram",
				`offset_seconds_bucket{le="0"} 1`,
				`offset_seconds_bucket{le="+Inf"} 2`,
				"offset_seconds_sum 0.5",
				"offset_seconds_count 2",
			},
		},
		{
			name: "When a summary or histogram metric has no value, it should drop it instead of forwarding it",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName(".*"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return map[string]*dto.MetricFamily{
					"empty_summary":   {Name: ptr.To("empty_summary"), Type: dto.MetricType_SUMMARY.Enum(), Metric: []*dto.Metric{{}}},
					"empty_histogram": {Name: ptr.To("empty_histogram"), Type: dto.MetricType_HISTOGRAM.Enum(), Metric: []*dto.Metric{{}}},
				}
			},
			wantLines: nil,
		},
		{
			name: "When a flattened histogram sample name collides with another family, it should drop the flattened samples",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("latency_seconds_bucket"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return map[string]*dto.MetricFamily{
					"latency_seconds":        histogramWithoutInfBucket("latency_seconds"),
					"latency_seconds_bucket": gaugeFamily("latency_seconds_bucket", 7),
				}
			},
			wantLines: []string{
				"# TYPE latency_seconds_bucket gauge",
				"latency_seconds_bucket 7",
			},
		},
		{
			name: "When flattened samples of two families map to the same name, it should drop both",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				keepName("a_sum"),
			},
			families: func(t *testing.T) map[string]*dto.MetricFamily {
				return collidingFamilies()
			},
			wantLines: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			families := parseFamilies(t, testExposition)
			if tt.families != nil {
				families = tt.families(t)
			}
			cfg := ComponentConfig{sampleFilter: newSampleFilter(logr.Discard(), "test-component", tt.mode, tt.rules)}

			got := NewFilter(logr.Discard()).Apply("test-component", cfg, families)
			g.Expect(renderFamilies(t, got)).To(Equal(tt.wantLines))
		})
	}

	t.Run("When a collision is evaluated repeatedly, it should give the same result regardless of map order and log once", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var records []string
		log := funcr.New(func(_, args string) { records = append(records, args) }, funcr.Options{})
		cfg := ComponentConfig{sampleFilter: newSampleFilter(log, "test-component", MetricsFilterRelabel,
			[]prometheusoperatorv1.RelabelConfig{keepName("a_sum|b_.*")})}
		filter := NewFilter(log)

		for range 25 {
			families := collidingFamilies()
			g.Expect(renderFamilies(t, filter.Apply("test-component", cfg, families))).To(Equal([]string{
				"# TYPE b_total counter",
				"b_total 6",
			}))
		}
		g.Expect(countContaining(records, "collides")).To(Equal(1))
	})

	t.Run("When the filter is shared by concurrent requests, it should filter each scrape independently", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cfg := ComponentConfig{sampleFilter: newSampleFilter(logr.Discard(), "test-component", MetricsFilterRelabel,
			[]prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        `apiserver_request_duration_seconds_bucket;(0\.1|\+Inf)`,
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			}})}
		filter := NewFilter(logr.Discard())

		inputs := make([]map[string]*dto.MetricFamily, 16)
		for i := range inputs {
			inputs[i] = parseFamilies(t, testExposition)
		}
		results := make([]map[string]*dto.MetricFamily, len(inputs))
		var wg sync.WaitGroup
		for i := range inputs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = filter.Apply("test-component", cfg, inputs[i])
			}()
		}
		wg.Wait()
		for _, result := range results {
			g.Expect(renderFamilies(t, result)).To(Equal([]string{
				"# TYPE apiserver_request_duration_seconds_bucket untyped",
				`apiserver_request_duration_seconds_bucket{verb="GET",le="0.1"} 5`,
				`apiserver_request_duration_seconds_bucket{verb="GET",le="+Inf"} 10`,
			}))
		}
	})
}

func TestNewSampleFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mode               MetricsFilterMode
		rules              []prometheusoperatorv1.RelabelConfig
		wantNil            bool
		wantForwardNothing bool
		wantRules          []relabelRule
		wantLogs           map[string]int
	}{
		{
			name:    "When the mode is unset, it should return no filter",
			mode:    "",
			wantNil: true,
		},
		{
			name:     "When the mode is None with rules, it should return no filter and log that the rules are ignored",
			mode:     MetricsFilterNone,
			rules:    []prometheusoperatorv1.RelabelConfig{{Action: "keep", Regex: "x"}},
			wantNil:  true,
			wantLogs: map[string]int{"forwards all samples": 1},
		},
		{
			name:               "When the mode is Relabel without rules, it should forward nothing",
			mode:               MetricsFilterRelabel,
			wantForwardNothing: true,
		},
		{
			name:               "When the mode is Relabel with an empty rule list, it should forward nothing",
			mode:               MetricsFilterRelabel,
			rules:              []prometheusoperatorv1.RelabelConfig{},
			wantForwardNothing: true,
		},
		{
			name:               "When the mode is unknown, it should forward nothing and log an error",
			mode:               MetricsFilterMode("Bogus"),
			wantForwardNothing: true,
			wantLogs:           map[string]int{`unknown metrics filter \"Bogus\"`: 1},
		},
		{
			name: "When actions use mixed case, it should compile them as keep and drop with Prometheus defaults",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "Keep", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
				{Action: "DROP", Regex: "b", Separator: ptr.To(""), SourceLabels: []prometheusoperatorv1.LabelName{"x", "y"}},
			},
			wantRules: []relabelRule{
				{keep: true, sourceLabels: []string{"__name__"}, separator: ";"},
				{keep: false, sourceLabels: []string{"x", "y"}, separator: ""},
			},
		},
		{
			name: "When only unsupported actions are configured, it should keep no rules, forward samples and log each action once",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "labeldrop", Regex: "a"},
				{Action: "LabelDrop", Regex: "b"},
				{TargetLabel: "c", Replacement: ptr.To("d")},
				{Action: "hashmod", Modulus: 2, TargetLabel: "e"},
			},
			wantRules: nil,
			wantLogs: map[string]int{
				`"action"="labeldrop"`: 1,
				`"action"="replace"`:   1,
				`"action"="hashmod"`:   1,
			},
		},
		{
			name: "When a keep regex does not compile, it should forward nothing and log the error",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "keep", Regex: "(", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
			},
			wantForwardNothing: true,
			wantLogs:           map[string]int{"Invalid metric relabel regex": 1},
		},
		{
			name: "When a keep regex is invalid before anchoring, it should forward nothing and log the error",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "keep", Regex: "foo)(bar", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
			},
			wantForwardNothing: true,
			wantLogs:           map[string]int{"Invalid metric relabel regex": 1},
		},
		{
			name: "When an unsupported action has an invalid regex, it should ignore the rule",
			mode: MetricsFilterRelabel,
			rules: []prometheusoperatorv1.RelabelConfig{
				{Action: "labelkeep", Regex: "("},
				{Action: "keep", Regex: "a", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
			},
			wantRules: []relabelRule{{keep: true, sourceLabels: []string{"__name__"}, separator: ";"}},
			wantLogs:  map[string]int{`"action"="labelkeep"`: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			var records []string
			log := funcr.New(func(_, args string) { records = append(records, args) }, funcr.Options{})

			got := newSampleFilter(log, "test-component", tt.mode, tt.rules)

			for substr, count := range tt.wantLogs {
				g.Expect(countContaining(records, substr)).To(Equal(count), "log records: %v", records)
			}
			if tt.wantNil {
				g.Expect(got).To(BeNil())
				return
			}
			g.Expect(got).NotTo(BeNil())
			g.Expect(got.forwardNothing).To(Equal(tt.wantForwardNothing))
			if tt.wantForwardNothing {
				return
			}
			g.Expect(got.rules).To(HaveLen(len(tt.wantRules)))
			for i, want := range tt.wantRules {
				g.Expect(got.rules[i].keep).To(Equal(want.keep))
				g.Expect(got.rules[i].sourceLabels).To(Equal(want.sourceLabels))
				g.Expect(got.rules[i].separator).To(Equal(want.separator))
			}
		})
	}

	t.Run("When a regex is configured, it should be anchored and default to (.*)", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		got := newSampleFilter(logr.Discard(), "test-component", MetricsFilterRelabel, []prometheusoperatorv1.RelabelConfig{
			{Action: "keep", Regex: "a|b", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
			{Action: "keep", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
		})
		g.Expect(got.rules).To(HaveLen(2))
		g.Expect(got.rules[0].regex.String()).To(Equal("^(?s:a|b)$"))
		g.Expect(got.rules[0].regex.MatchString("ab")).To(BeFalse())
		g.Expect(got.rules[0].regex.MatchString("b")).To(BeTrue())
		g.Expect(got.rules[1].regex.String()).To(Equal("^(?s:(.*))$"))
	})
}

func concatLines(groups ...[]string) []string {
	var lines []string
	for _, group := range groups {
		lines = append(lines, group...)
	}
	return lines
}

func countContaining(records []string, substr string) int {
	count := 0
	for _, r := range records {
		if strings.Contains(r, substr) {
			count++
		}
	}
	return count
}

// parseFamilies parses a text exposition the same way the scraper does.
func parseFamilies(t *testing.T, text string) map[string]*dto.MetricFamily {
	t.Helper()
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(text))
	if err != nil {
		t.Fatalf("failed to parse exposition: %v", err)
	}
	return families
}

// renderFamilies encodes the families in the text format, sorted by family
// name, and returns the TYPE and sample lines.
func renderFamilies(t *testing.T, families map[string]*dto.MetricFamily) []string {
	t.Helper()
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	encoder := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, name := range names {
		if err := encoder.Encode(families[name]); err != nil {
			t.Fatalf("failed to encode family %s: %v", name, err)
		}
	}

	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" || strings.HasPrefix(line, "# HELP ") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// histogramWithoutInfBucket returns a histogram family whose only explicit
// bucket is le="1", so the text encoder adds the +Inf bucket.
func histogramWithoutInfBucket(name string) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: ptr.To(name),
		Type: dto.MetricType_HISTOGRAM.Enum(),
		Metric: []*dto.Metric{{
			Histogram: &dto.Histogram{
				SampleCount: ptr.To[uint64](4),
				SampleSum:   ptr.To(2.0),
				Bucket:      []*dto.Bucket{{UpperBound: ptr.To(1.0), CumulativeCount: ptr.To[uint64](3)}},
			},
		}},
	}
}

// collidingFamilies returns a histogram "a" and a summary "a_sum" whose
// flattened samples share the name a_sum, plus an unrelated counter.
func collidingFamilies() map[string]*dto.MetricFamily {
	return map[string]*dto.MetricFamily{
		"a": {
			Name: ptr.To("a"),
			Type: dto.MetricType_HISTOGRAM.Enum(),
			Metric: []*dto.Metric{{Histogram: &dto.Histogram{
				SampleCount: ptr.To[uint64](1),
				SampleSum:   ptr.To(2.0),
				Bucket:      []*dto.Bucket{{UpperBound: ptr.To(math.Inf(+1)), CumulativeCount: ptr.To[uint64](1)}},
			}}},
		},
		"a_sum": {
			Name: ptr.To("a_sum"),
			Type: dto.MetricType_SUMMARY.Enum(),
			Metric: []*dto.Metric{{Summary: &dto.Summary{
				SampleCount: ptr.To[uint64](5),
				SampleSum:   ptr.To(4.0),
				Quantile:    []*dto.Quantile{{Quantile: ptr.To(0.5), Value: ptr.To(3.0)}},
			}}},
		},
		"b_total": {
			Name:   ptr.To("b_total"),
			Type:   dto.MetricType_COUNTER.Enum(),
			Metric: []*dto.Metric{{Counter: &dto.Counter{Value: ptr.To(6.0)}}},
		},
	}
}

func gaugeFamily(name string, value float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name:   ptr.To(name),
		Type:   dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{{Gauge: &dto.Gauge{Value: ptr.To(value)}}},
	}
}

func TestFormatLabelFloat(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value float64
		want  string
	}{
		{value: 0.005, want: "0.005"},
		{value: 1, want: "1"},
		{value: 2.5e+06, want: "2.5e+06"},
		{value: math.Inf(+1), want: "+Inf"},
		{value: math.Copysign(0, -1), want: "0"},
	} {
		t.Run(fmt.Sprintf("When the value is %v, it should format it like the text encoder", tc.value), func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(formatLabelFloat(tc.value)).To(Equal(tc.want))
		})
	}
}
