package metricsproxy

import (
	"regexp"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/metrics"

	dto "github.com/prometheus/client_model/go"
)

func TestFilter_Apply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		metricsSet    metrics.MetricsSet
		componentName string
		families      map[string]*dto.MetricFamily
		wantNames     []string
	}{
		{
			name:          "When MetricsSetAll is used, it should return all metric families",
			metricsSet:    metrics.MetricsSetAll,
			componentName: "kube-apiserver",
			families: map[string]*dto.MetricFamily{
				"apiserver_storage_objects":           createMetricFamily("apiserver_storage_objects"),
				"apiserver_request_total":             createMetricFamily("apiserver_request_total"),
				"some_unrelated_metric":               createMetricFamily("some_unrelated_metric"),
				"apiserver_current_inflight_requests": createMetricFamily("apiserver_current_inflight_requests"),
			},
			wantNames: []string{
				"apiserver_storage_objects",
				"apiserver_request_total",
				"some_unrelated_metric",
				"apiserver_current_inflight_requests",
			},
		},
		{
			name:          "When MetricsSetTelemetry is used with kube-apiserver, it should filter correctly",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "kube-apiserver",
			families: map[string]*dto.MetricFamily{
				"apiserver_storage_objects":           createMetricFamily("apiserver_storage_objects"),
				"apiserver_request_total":             createMetricFamily("apiserver_request_total"),
				"apiserver_current_inflight_requests": createMetricFamily("apiserver_current_inflight_requests"),
				"some_unrelated_metric":               createMetricFamily("some_unrelated_metric"),
				"etcd_debugging_count":                createMetricFamily("etcd_debugging_count"),
			},
			wantNames: []string{
				"apiserver_storage_objects",
				"apiserver_request_total",
				"apiserver_current_inflight_requests",
			},
		},
		{
			name:          "When MetricsSetTelemetry is used with etcd, it should filter correctly",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "etcd",
			families: map[string]*dto.MetricFamily{
				"etcd_disk_wal_fsync_duration_seconds_bucket": createMetricFamily("etcd_disk_wal_fsync_duration_seconds_bucket"),
				"etcd_mvcc_db_total_size_in_bytes":            createMetricFamily("etcd_mvcc_db_total_size_in_bytes"),
				"etcd_server_leader_changes_seen_total":       createMetricFamily("etcd_server_leader_changes_seen_total"),
				"etcd_some_other_metric":                      createMetricFamily("etcd_some_other_metric"),
			},
			wantNames: []string{
				"etcd_disk_wal_fsync_duration_seconds_bucket",
				"etcd_mvcc_db_total_size_in_bytes",
				"etcd_server_leader_changes_seen_total",
			},
		},
		{
			name:          "When MetricsSetTelemetry is used with kube-controller-manager, it should filter correctly",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "kube-controller-manager",
			families: map[string]*dto.MetricFamily{
				"pv_collector_total_pv_count": createMetricFamily("pv_collector_total_pv_count"),
				"some_other_metric":           createMetricFamily("some_other_metric"),
				"rest_client_requests_total":  createMetricFamily("rest_client_requests_total"),
			},
			wantNames: []string{
				"pv_collector_total_pv_count",
			},
		},
		{
			name:          "When unknown component is provided, it should return all families",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "unknown-component",
			families: map[string]*dto.MetricFamily{
				"metric_one":   createMetricFamily("metric_one"),
				"metric_two":   createMetricFamily("metric_two"),
				"metric_three": createMetricFamily("metric_three"),
			},
			wantNames: []string{
				"metric_one",
				"metric_two",
				"metric_three",
			},
		},
		{
			name:          "When MetricsSetTelemetry is used with cluster-version-operator, it should filter correctly",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "cluster-version-operator",
			families: map[string]*dto.MetricFamily{
				"cluster_version":                   createMetricFamily("cluster_version"),
				"cluster_version_available_updates": createMetricFamily("cluster_version_available_updates"),
				"cluster_operator_up":               createMetricFamily("cluster_operator_up"),
				"some_unrelated_cvo_metric":         createMetricFamily("some_unrelated_cvo_metric"),
			},
			wantNames: []string{
				"cluster_version",
				"cluster_version_available_updates",
				"cluster_operator_up",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := NewFilter(tt.metricsSet)
			got := f.Apply(tt.componentName, tt.families)

			if len(got) != len(tt.wantNames) {
				t.Errorf("Apply() returned %d families, want %d", len(got), len(tt.wantNames))
			}

			for _, name := range tt.wantNames {
				if _, ok := got[name]; !ok {
					t.Errorf("Apply() missing expected metric family: %s", name)
				}
			}

			for name := range got {
				found := false
				for _, wantName := range tt.wantNames {
					if name == wantName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Apply() returned unexpected metric family: %s", name)
				}
			}
		})
	}
}

func TestFilter_getOrCompile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		metricsSet    metrics.MetricsSet
		componentName string
		wantNil       bool
		wantCached    bool
	}{
		{
			name:          "When component has keep regex, it should cache compiled regex",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "kube-apiserver",
			wantNil:       false,
			wantCached:    true,
		},
		{
			name:          "When component has no keep regex, it should cache nil",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "unknown-component",
			wantNil:       true,
			wantCached:    true,
		},
		{
			name:          "When called twice for same component, it should return cached regex",
			metricsSet:    metrics.MetricsSetTelemetry,
			componentName: "etcd",
			wantNil:       false,
			wantCached:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := NewFilter(tt.metricsSet)

			// First call
			got1 := f.getOrCompile(tt.componentName)
			if (got1 == nil) != tt.wantNil {
				t.Errorf("getOrCompile() first call returned nil=%v, want nil=%v", got1 == nil, tt.wantNil)
			}

			// Second call - should return cached value
			got2 := f.getOrCompile(tt.componentName)
			if got1 != got2 {
				t.Errorf("getOrCompile() second call returned different value, cache not working")
			}

			// Verify cache contains the entry
			f.mu.RLock()
			cached, ok := f.cache[tt.componentName]
			f.mu.RUnlock()

			if !ok {
				t.Errorf("getOrCompile() did not cache the result for component: %s", tt.componentName)
			}

			if cached != got1 {
				t.Errorf("getOrCompile() cached value differs from returned value")
			}
		})
	}
}

func TestFilter_getOrCompile_HandlesEmptyRegex(t *testing.T) {
	t.Parallel()

	f := NewFilter(metrics.MetricsSetTelemetry)

	// Test with a component that has no keep regex
	result := f.getOrCompile("nonexistent-component")

	if result != nil {
		t.Errorf("When regex string is empty, it should return nil, got: %v", result)
	}

	// Verify it was cached as nil
	f.mu.RLock()
	cached, ok := f.cache["nonexistent-component"]
	f.mu.RUnlock()

	if !ok {
		t.Error("When regex string is empty, it should cache nil result")
	}

	if cached != nil {
		t.Errorf("When regex string is empty, cached value should be nil, got: %v", cached)
	}
}

func TestGetKeepRegexForComponent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		componentName string
		metricsSet    metrics.MetricsSet
		wantEmpty     bool
		wantContains  string
	}{
		{
			name:          "When kube-apiserver with Telemetry, it should return non-empty regex",
			componentName: "kube-apiserver",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "apiserver_storage_objects",
		},
		{
			name:          "When etcd with Telemetry, it should return non-empty regex",
			componentName: "etcd",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "etcd_disk_wal_fsync_duration_seconds_bucket",
		},
		{
			name:          "When kube-controller-manager with Telemetry, it should return non-empty regex",
			componentName: "kube-controller-manager",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "pv_collector_total_pv_count",
		},
		{
			name:          "When openshift-apiserver with Telemetry, it should return non-empty regex",
			componentName: "openshift-apiserver",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "apiserver_storage_objects",
		},
		{
			name:          "When openshift-controller-manager with Telemetry, it should return non-empty regex",
			componentName: "openshift-controller-manager",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "openshift_build_status_phase_total",
		},
		{
			name:          "When cluster-version-operator with Telemetry, it should return non-empty regex",
			componentName: "cluster-version-operator",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "cluster_version",
		},
		{
			name:          "When olm-operator with Telemetry, it should return non-empty regex",
			componentName: "olm-operator",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "csv_succeeded",
		},
		{
			name:          "When catalog-operator with Telemetry, it should return non-empty regex",
			componentName: "catalog-operator",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "subscription_sync_total",
		},
		{
			name:          "When node-tuning-operator with Telemetry, it should return non-empty regex",
			componentName: "node-tuning-operator",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     false,
			wantContains:  "nto_profile_calculated_total",
		},
		{
			name:          "When unknown component, it should return empty string",
			componentName: "unknown-component",
			metricsSet:    metrics.MetricsSetTelemetry,
			wantEmpty:     true,
		},
		{
			name:          "When kube-apiserver with MetricsSetAll, it should return empty string",
			componentName: "kube-apiserver",
			metricsSet:    metrics.MetricsSetAll,
			wantEmpty:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := getKeepRegexForComponent(tt.componentName, tt.metricsSet)

			if tt.wantEmpty {
				if got != "" {
					t.Errorf("getKeepRegexForComponent() = %q, want empty string", got)
				}
			} else {
				if got == "" {
					t.Error("getKeepRegexForComponent() returned empty string, want non-empty")
				}
				if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
					t.Errorf("getKeepRegexForComponent() = %q, want to contain %q", got, tt.wantContains)
				}
			}
		})
	}
}

func TestFilter_Apply_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	f := NewFilter(metrics.MetricsSetTelemetry)
	families := map[string]*dto.MetricFamily{
		"apiserver_storage_objects": createMetricFamily("apiserver_storage_objects"),
		"apiserver_request_total":   createMetricFamily("apiserver_request_total"),
	}

	// Test concurrent access to the filter
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = f.Apply("kube-apiserver", families)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestGetKeepRegexForComponent_ValidRegex(t *testing.T) {
	t.Parallel()

	components := []string{
		"kube-apiserver",
		"etcd",
		"kube-controller-manager",
		"openshift-apiserver",
		"openshift-controller-manager",
		"openshift-route-controller-manager",
		"cluster-version-operator",
		"olm-operator",
		"catalog-operator",
		"node-tuning-operator",
	}

	for _, component := range components {
		t.Run(component, func(t *testing.T) {
			t.Parallel()

			regexStr := getKeepRegexForComponent(component, metrics.MetricsSetTelemetry)
			if regexStr == "" {
				t.Skip("Component has no keep regex for Telemetry set")
			}

			// Verify the regex compiles successfully
			fullRegex := "^(" + regexStr + ")$"
			_, err := regexp.Compile(fullRegex)
			if err != nil {
				t.Errorf("When regex is returned for %s, it should be valid, got error: %v", component, err)
			}
		})
	}
}

// Helper function to create a simple MetricFamily for testing
func createMetricFamily(name string) *dto.MetricFamily {
	metricType := dto.MetricType_GAUGE
	return &dto.MetricFamily{
		Name: &name,
		Type: &metricType,
	}
}
