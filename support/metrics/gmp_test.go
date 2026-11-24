package metrics

import (
	"fmt"
	"testing"

	"github.com/openshift/hypershift/support/gmpmonitoring"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adaptExpected translates CoreOS relabel configs the way AdaptGMPPodMonitoring
// does, so an assertion that expects an unchanged adapter output cannot pass
// because both sides share a translation bug.
func adaptExpected(t *testing.T, source []prometheusoperatorv1.RelabelConfig) []gmpmonitoring.RelabelingRule {
	t.Helper()
	if len(source) == 0 {
		return nil
	}
	rules, err := ToGMPRelabelingRules(source)
	require.NoError(t, err)
	return rules
}

func clusterIDRule(clusterID string) gmpmonitoring.RelabelingRule {
	return gmpmonitoring.RelabelingRule{
		TargetLabel: GMPClusterIDLabel,
		Replacement: clusterID,
		Action:      "replace",
	}
}

func TestToGMPRelabelingRules(t *testing.T) {
	tests := []struct {
		name        string
		source      []prometheusoperatorv1.RelabelConfig
		expected    []gmpmonitoring.RelabelingRule
		expectedErr string
	}{
		{
			name:     "When the source is empty, it should return no rules",
			source:   nil,
			expected: nil,
		},
		{
			name: "When a rule carries every supported field, it should preserve their meaning",
			source: []prometheusoperatorv1.RelabelConfig{{
				SourceLabels: []prometheusoperatorv1.LabelName{"__meta_kubernetes_pod_label_app"},
				Separator:    ptr.To(";"),
				TargetLabel:  "app",
				Regex:        "app-(.+)",
				Replacement:  ptr.To("$1"),
				Action:       "Replace",
			}},
			expected: []gmpmonitoring.RelabelingRule{{
				SourceLabels: []string{"__meta_kubernetes_pod_label_app"},
				Separator:    ";",
				TargetLabel:  "app",
				Regex:        "app-(.+)",
				Replacement:  "$1",
				Action:       "replace",
			}},
		},
		{
			name: "When a rule omits separator and replacement, it should rely on the GMP defaults",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "drop",
				Regex:  "up",
			}},
			expected: []gmpmonitoring.RelabelingRule{{
				Action: "drop",
				Regex:  "up",
			}},
		},
		{
			name: "When a rule uses labeldrop with a regex, it should translate it",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labeldrop",
				Regex:  "__meta_kubernetes_.*",
			}},
			expected: []gmpmonitoring.RelabelingRule{{
				Action: "labeldrop",
				Regex:  "__meta_kubernetes_.*",
			}},
		},
		{
			name: "When a rule uses an action GMP does not define, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labelmap",
			}},
			expectedErr: `action "labelmap" is not supported by GMP`,
		},
		{
			name: "When a rule sets an explicitly empty separator, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Separator: ptr.To(""),
			}},
			expectedErr: "empty separator cannot be preserved by GMP",
		},
		{
			name: "When a rule sets an explicitly empty replacement, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Replacement: ptr.To(""),
			}},
			expectedErr: "empty replacement cannot be preserved by GMP",
		},
		{
			name: "When a rule uses labeldrop without a regex, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labeldrop",
			}},
			expectedErr: "labeldrop must set a regex",
		},
		{
			name: "When CoreOS drops every metric, it should emit the GMP-safe keep-nothing filter",
			source: []prometheusoperatorv1.RelabelConfig{{
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
				Regex:        "(.*)",
				Action:       "drop",
			}},
			expected: []gmpmonitoring.RelabelingRule{{
				SourceLabels: []string{"__name__"},
				Regex:        "^$",
				Action:       "keep",
			}},
		},
		{
			name: "When a rule keeps every metric, it should emit no rule",
			source: []prometheusoperatorv1.RelabelConfig{{
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
				Regex:        "(.*)",
				Action:       "keep",
			}},
			expected: nil,
		},
		{
			name: "When a narrow regex matches a protected label name, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
				Regex:        "name.*",
				Action:       "drop",
			}},
			expectedErr: `matches protected label "namespace", which GMP rejects`,
		},
		{
			name: "When a rule relabels onto a label GMP protects, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				TargetLabel: "namespace",
				Replacement: ptr.To("openshift-monitoring"),
			}},
			expectedErr: `target label "namespace" is protected by GMP`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := ToGMPRelabelingRules(tc.source)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}

	// An adapter rule GMP rejects fails the whole component reconcile at
	// runtime, so every registered adapter must translate for every MetricsSet.
	for component, adapters := range GMPMetricRelabelingAdapters {
		for i, adapter := range adapters {
			for _, set := range []MetricsSet{MetricsSetTelemetry, MetricsSetSRE, MetricsSetAll} {
				t.Run(fmt.Sprintf("When %s endpoint %d uses the %s set, it should translate to GMP rules", component, i, set), func(t *testing.T) {
					_, err := ToGMPRelabelingRules(adapter(set))

					require.NoError(t, err)
				})
			}
		}
	}
}

func TestAdaptGMPPodMonitoring(t *testing.T) {
	t.Run("When the staged endpoint carries a namespace template, it should substitute the live namespace", func(t *testing.T) {
		podMonitoring := &gmpmonitoring.PodMonitoring{
			ObjectMeta: metav1.ObjectMeta{Namespace: GMPNamespacePlaceholder},
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{
					Port: intstr.FromString("https"),
					HTTPClientConfig: gmpmonitoring.HTTPClientConfig{
						TLS: &gmpmonitoring.TLS{
							ServerName: "etcd-client." + GMPNamespacePlaceholder + ".svc",
						},
					},
				}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("etcd", podMonitoring, MetricsSetAll, "cluster-123", "clusters-example"))

		assert.Equal(t, "clusters-example", podMonitoring.Namespace)
		assert.Equal(t, "etcd-client.clusters-example.svc", podMonitoring.Spec.Endpoints[0].TLS.ServerName)
	})

	t.Run("When a component has a MetricsSet adapter, it should replace the staged relabeling with the adapter output", func(t *testing.T) {
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{
					// The staged block is a snapshot of the Telemetry set; it must
					// not survive a reconcile for a different set.
					MetricRelabeling: []gmpmonitoring.RelabelingRule{{Action: "drop", Regex: "stale"}},
				}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("openshift-apiserver", podMonitoring, MetricsSetAll, "cluster-123", "clusters-example"))

		expected := adaptExpected(t, OpenShiftAPIServerRelabelConfigs(MetricsSetAll))
		expected = append(expected, clusterIDRule("cluster-123"))
		assert.Equal(t, expected, podMonitoring.Spec.Endpoints[0].MetricRelabeling)
	})

	t.Run("When the staged endpoint carries target-label rules, it should keep them ahead of the adapter output", func(t *testing.T) {
		targetLabels := []gmpmonitoring.RelabelingRule{
			{TargetLabel: "service", Replacement: "openshift-apiserver", Action: "replace"},
			{TargetLabel: "endpoint", Replacement: "https", Action: "replace"},
		}
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{
					MetricRelabeling: append(append([]gmpmonitoring.RelabelingRule{}, targetLabels...),
						gmpmonitoring.RelabelingRule{Action: "drop", Regex: "stale"},
						// A rule that copies a value is a filter rule, not a constant target label.
						gmpmonitoring.RelabelingRule{SourceLabels: []string{"code"}, TargetLabel: "endpoint", Action: "replace"},
					),
				}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("openshift-apiserver", podMonitoring, MetricsSetAll, "cluster-123", "clusters-example"))

		expected := append(append([]gmpmonitoring.RelabelingRule{}, targetLabels...), adaptExpected(t, OpenShiftAPIServerRelabelConfigs(MetricsSetAll))...)
		expected = append(expected, clusterIDRule("cluster-123"))
		assert.Equal(t, expected, podMonitoring.Spec.Endpoints[0].MetricRelabeling)
	})

	t.Run("When the staged endpoint pins the container label, it should keep that rule ahead of the adapter output", func(t *testing.T) {
		// GMP fills container only for a port referenced by name, so an endpoint
		// written as a port number carries a constant container rule.
		targetLabels := []gmpmonitoring.RelabelingRule{
			{TargetLabel: "service", Replacement: "kube-scheduler", Action: "replace"},
			{TargetLabel: "endpoint", Replacement: "client", Action: "replace"},
			{TargetLabel: "container", Replacement: "kube-scheduler", Action: "replace"},
		}
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{}, {
					MetricRelabeling: append(append([]gmpmonitoring.RelabelingRule{}, targetLabels...),
						gmpmonitoring.RelabelingRule{Action: "drop", Regex: "stale"}),
				}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("kube-scheduler", podMonitoring, MetricsSetTelemetry, "", "clusters-example"))

		expected := append(append([]gmpmonitoring.RelabelingRule{}, targetLabels...),
			adaptExpected(t, SchedulerResourceRelabelConfigs(MetricsSetTelemetry))...)
		assert.Equal(t, expected, podMonitoring.Spec.Endpoints[1].MetricRelabeling)
	})

	t.Run("When a component has one adapter per endpoint, it should index the adapters by endpoint", func(t *testing.T) {
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{}, {}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("kube-scheduler", podMonitoring, MetricsSetTelemetry, "", "clusters-example"))

		assert.Equal(t, adaptExpected(t, SchedulerRelabelConfigs(MetricsSetTelemetry)), podMonitoring.Spec.Endpoints[0].MetricRelabeling)
		assert.Equal(t, adaptExpected(t, SchedulerResourceRelabelConfigs(MetricsSetTelemetry)), podMonitoring.Spec.Endpoints[1].MetricRelabeling)
	})

	t.Run("When a component has no adapter, it should keep the staged relabeling and append only the cluster ID", func(t *testing.T) {
		staged := []gmpmonitoring.RelabelingRule{{Action: "drop", Regex: "stale"}}
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{MetricRelabeling: staged}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("cluster-autoscaler", podMonitoring, MetricsSetAll, "cluster-123", "clusters-example"))

		expected := append(adaptExpected(t, nil), staged...)
		expected = append(expected, clusterIDRule("cluster-123"))
		assert.Equal(t, expected, podMonitoring.Spec.Endpoints[0].MetricRelabeling)
	})

	t.Run("When the cluster ID is empty, it should not add a cluster ID label", func(t *testing.T) {
		podMonitoring := &gmpmonitoring.PodMonitoring{
			Spec: gmpmonitoring.PodMonitoringSpec{
				Endpoints: []gmpmonitoring.ScrapeEndpoint{{}},
			},
		}

		require.NoError(t, AdaptGMPPodMonitoring("cluster-autoscaler", podMonitoring, MetricsSetAll, "", "clusters-example"))

		assert.Empty(t, podMonitoring.Spec.Endpoints[0].MetricRelabeling)
	})

	t.Run("When the PodMonitoring is nil, it should return an error", func(t *testing.T) {
		require.Error(t, AdaptGMPPodMonitoring("etcd", nil, MetricsSetAll, "cluster-123", "clusters-example"))
	})
}

func TestApplyGMPClusterIDLabel(t *testing.T) {
	t.Run("When a cluster ID is set, it should append a replace rule for _id", func(t *testing.T) {
		endpoint := &gmpmonitoring.ScrapeEndpoint{}

		ApplyGMPClusterIDLabel(endpoint, "cluster-123")

		assert.Equal(t, []gmpmonitoring.RelabelingRule{clusterIDRule("cluster-123")}, endpoint.MetricRelabeling)
	})

	t.Run("When a cluster ID is empty, it should leave the endpoint unchanged", func(t *testing.T) {
		endpoint := &gmpmonitoring.ScrapeEndpoint{}

		ApplyGMPClusterIDLabel(endpoint, "")

		assert.Empty(t, endpoint.MetricRelabeling)
	})
}
