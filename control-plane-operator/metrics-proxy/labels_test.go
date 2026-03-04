package metricsproxy

import (
	"sort"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestNewLabeler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When namespace is provided, it should set namespace correctly",
			namespace: "test-namespace",
		},
		{
			name:      "When namespace is empty, it should set empty namespace",
			namespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			labeler := NewLabeler(tt.namespace)

			if labeler == nil {
				t.Fatal("Expected non-nil labeler")
			}
			if labeler.namespace != tt.namespace {
				t.Errorf("Expected namespace %q, got %q", tt.namespace, labeler.namespace)
			}
		})
	}
}

func TestHasLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metric   *dto.Metric
		label    string
		expected bool
	}{
		{
			name: "When label exists, it should return true",
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{Name: stringPtr("foo"), Value: stringPtr("bar")},
					{Name: stringPtr("baz"), Value: stringPtr("qux")},
				},
			},
			label:    "foo",
			expected: true,
		},
		{
			name: "When label does not exist, it should return false",
			metric: &dto.Metric{
				Label: []*dto.LabelPair{
					{Name: stringPtr("foo"), Value: stringPtr("bar")},
				},
			},
			label:    "nonexistent",
			expected: false,
		},
		{
			name: "When metric has no labels, it should return false",
			metric: &dto.Metric{
				Label: []*dto.LabelPair{},
			},
			label:    "foo",
			expected: false,
		},
		{
			name: "When metric has nil labels, it should return false",
			metric: &dto.Metric{
				Label: nil,
			},
			label:    "foo",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := hasLabel(tt.metric, tt.label)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestInject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		namespace     string
		families      map[string]*dto.MetricFamily
		target        ScrapeTarget
		componentName string
		serviceName   string
		expected      map[string]*dto.MetricFamily
	}{
		{
			name:      "When metric family has metrics, it should add all expected labels",
			namespace: "test-ns",
			families: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_COUNTER),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{},
						},
					},
				},
			},
			target: ScrapeTarget{
				PodName: "test-pod",
				PodIP:   "10.0.0.1",
				Port:    8080,
			},
			componentName: "test-component",
			serviceName:   "test-service",
			expected: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_COUNTER),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("pod"), Value: stringPtr("test-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("test-ns")},
								{Name: stringPtr("job"), Value: stringPtr("test-component")},
								{Name: stringPtr("service"), Value: stringPtr("test-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("10.0.0.1:8080")},
							},
						},
					},
				},
			},
		},
		{
			name:      "When metric already has labels, it should not overwrite existing labels",
			namespace: "test-ns",
			families: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_GAUGE),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("existing"), Value: stringPtr("value")},
								{Name: stringPtr("pod"), Value: stringPtr("existing-pod")},
							},
						},
					},
				},
			},
			target: ScrapeTarget{
				PodName: "test-pod",
				PodIP:   "10.0.0.2",
				Port:    9090,
			},
			componentName: "test-component",
			serviceName:   "test-service",
			expected: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_GAUGE),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("existing"), Value: stringPtr("value")},
								{Name: stringPtr("pod"), Value: stringPtr("existing-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("test-ns")},
								{Name: stringPtr("job"), Value: stringPtr("test-component")},
								{Name: stringPtr("service"), Value: stringPtr("test-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("10.0.0.2:9090")},
							},
						},
					},
				},
			},
		},
		{
			name:      "When metric family is empty, it should handle gracefully",
			namespace: "test-ns",
			families:  map[string]*dto.MetricFamily{},
			target: ScrapeTarget{
				PodName: "test-pod",
				PodIP:   "10.0.0.3",
				Port:    7070,
			},
			componentName: "test-component",
			serviceName:   "test-service",
			expected:      map[string]*dto.MetricFamily{},
		},
		{
			name:      "When metric family has multiple metrics, it should add labels to all metrics",
			namespace: "prod-ns",
			families: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_HISTOGRAM),
					Metric: []*dto.Metric{
						{Label: []*dto.LabelPair{}},
						{Label: []*dto.LabelPair{}},
					},
				},
			},
			target: ScrapeTarget{
				PodName: "multi-pod",
				PodIP:   "192.168.1.1",
				Port:    3000,
			},
			componentName: "multi-component",
			serviceName:   "multi-service",
			expected: map[string]*dto.MetricFamily{
				"test_metric": {
					Name: stringPtr("test_metric"),
					Type: metricTypePtr(dto.MetricType_HISTOGRAM),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("pod"), Value: stringPtr("multi-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("prod-ns")},
								{Name: stringPtr("job"), Value: stringPtr("multi-component")},
								{Name: stringPtr("service"), Value: stringPtr("multi-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("192.168.1.1:3000")},
							},
						},
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("pod"), Value: stringPtr("multi-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("prod-ns")},
								{Name: stringPtr("job"), Value: stringPtr("multi-component")},
								{Name: stringPtr("service"), Value: stringPtr("multi-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("192.168.1.1:3000")},
							},
						},
					},
				},
			},
		},
		{
			name:      "When multiple metric families exist, it should inject labels to all families",
			namespace: "test-ns",
			families: map[string]*dto.MetricFamily{
				"metric_one": {
					Name: stringPtr("metric_one"),
					Type: metricTypePtr(dto.MetricType_COUNTER),
					Metric: []*dto.Metric{
						{Label: []*dto.LabelPair{}},
					},
				},
				"metric_two": {
					Name: stringPtr("metric_two"),
					Type: metricTypePtr(dto.MetricType_GAUGE),
					Metric: []*dto.Metric{
						{Label: []*dto.LabelPair{}},
					},
				},
			},
			target: ScrapeTarget{
				PodName: "test-pod",
				PodIP:   "10.0.0.1",
				Port:    8080,
			},
			componentName: "test-component",
			serviceName:   "test-service",
			expected: map[string]*dto.MetricFamily{
				"metric_one": {
					Name: stringPtr("metric_one"),
					Type: metricTypePtr(dto.MetricType_COUNTER),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("pod"), Value: stringPtr("test-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("test-ns")},
								{Name: stringPtr("job"), Value: stringPtr("test-component")},
								{Name: stringPtr("service"), Value: stringPtr("test-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("10.0.0.1:8080")},
							},
						},
					},
				},
				"metric_two": {
					Name: stringPtr("metric_two"),
					Type: metricTypePtr(dto.MetricType_GAUGE),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{Name: stringPtr("pod"), Value: stringPtr("test-pod")},
								{Name: stringPtr("namespace"), Value: stringPtr("test-ns")},
								{Name: stringPtr("job"), Value: stringPtr("test-component")},
								{Name: stringPtr("service"), Value: stringPtr("test-service")},
								{Name: stringPtr("endpoint"), Value: stringPtr("metrics")},
								{Name: stringPtr("instance"), Value: stringPtr("10.0.0.1:8080")},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			labeler := NewLabeler(tt.namespace)
			result := labeler.Inject(tt.families, tt.target, tt.componentName, tt.serviceName)

			if err := compareMetricFamilies(tt.expected, result); err != nil {
				t.Errorf("Inject() mismatch: %v", err)
			}
		})
	}
}

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}

// metricTypePtr returns a pointer to a MetricType
func metricTypePtr(t dto.MetricType) *dto.MetricType {
	return &t
}

// compareMetricFamilies compares two metric family maps for equality
func compareMetricFamilies(expected, actual map[string]*dto.MetricFamily) error {
	if len(expected) != len(actual) {
		return &comparisonError{msg: "metric family count mismatch"}
	}

	for name, expectedFamily := range expected {
		actualFamily, ok := actual[name]
		if !ok {
			return &comparisonError{msg: "missing metric family: " + name}
		}

		if expectedFamily.GetName() != actualFamily.GetName() {
			return &comparisonError{msg: "metric family name mismatch"}
		}

		if expectedFamily.GetType() != actualFamily.GetType() {
			return &comparisonError{msg: "metric family type mismatch"}
		}

		if len(expectedFamily.Metric) != len(actualFamily.Metric) {
			return &comparisonError{msg: "metric count mismatch in family " + name}
		}

		for i, expectedMetric := range expectedFamily.Metric {
			actualMetric := actualFamily.Metric[i]

			if err := compareMetrics(expectedMetric, actualMetric); err != nil {
				return err
			}
		}
	}

	return nil
}

// compareMetrics compares two metrics for equality
func compareMetrics(expected, actual *dto.Metric) error {
	if len(expected.Label) != len(actual.Label) {
		return &comparisonError{msg: "label count mismatch"}
	}

	// Sort labels by name for consistent comparison
	expectedLabels := sortedLabels(expected.Label)
	actualLabels := sortedLabels(actual.Label)

	for i, expectedLabel := range expectedLabels {
		actualLabel := actualLabels[i]

		if expectedLabel.GetName() != actualLabel.GetName() {
			return &comparisonError{msg: "label name mismatch"}
		}

		if expectedLabel.GetValue() != actualLabel.GetValue() {
			return &comparisonError{msg: "label value mismatch for " + expectedLabel.GetName()}
		}
	}

	return nil
}

// sortedLabels returns a sorted copy of label pairs
func sortedLabels(labels []*dto.LabelPair) []*dto.LabelPair {
	sorted := make([]*dto.LabelPair, len(labels))
	copy(sorted, labels)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].GetName() < sorted[j].GetName()
	})
	return sorted
}

// comparisonError is a simple error type for comparison failures
type comparisonError struct {
	msg string
}

func (e *comparisonError) Error() string {
	return e.msg
}
