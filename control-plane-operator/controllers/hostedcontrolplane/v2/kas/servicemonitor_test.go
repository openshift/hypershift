package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestAdaptServiceMonitor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		metricsSet metrics.MetricsSet
		clusterID  string
		validate   func(*testing.T, *prometheusoperatorv1.ServiceMonitor, error)
	}{
		{
			name:       "When service monitor is adapted, it should set namespace selector",
			metricsSet: metrics.MetricsSetTelemetry,
			clusterID:  "test-cluster-id",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.NamespaceSelector.MatchNames).To(Equal([]string{"test-namespace"}))
			},
		},
		{
			name:       "When metrics set is Telemetry, it should apply Telemetry relabel configs",
			metricsSet: metrics.MetricsSetTelemetry,
			clusterID:  "test-cluster-id",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// Telemetry set produces 1 keep rule + 1 cluster ID relabel = 2 MetricRelabelConfigs
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs).To(HaveLen(2))
				g.Expect(string(sm.Spec.Endpoints[0].MetricRelabelConfigs[0].Action)).To(Equal("keep"))
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs[0].Regex).To(Equal("(apiserver_storage_objects|apiserver_request_total|apiserver_current_inflight_requests)"))
			},
		},
		{
			name:       "When service monitor is adapted, it should apply cluster ID label in both RelabelConfigs and MetricRelabelConfigs",
			metricsSet: metrics.MetricsSetTelemetry,
			clusterID:  "cluster-abc-123",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// Check cluster ID in RelabelConfigs
				foundInRelabel := false
				for _, config := range sm.Spec.Endpoints[0].RelabelConfigs {
					if config.TargetLabel == "_id" && config.Replacement != nil && *config.Replacement == "cluster-abc-123" {
						foundInRelabel = true
						break
					}
				}
				g.Expect(foundInRelabel).To(BeTrue(), "cluster ID label should be applied in RelabelConfigs")

				// Check cluster ID in MetricRelabelConfigs
				foundInMetricRelabel := false
				for _, config := range sm.Spec.Endpoints[0].MetricRelabelConfigs {
					if config.TargetLabel == "_id" && config.Replacement != nil && *config.Replacement == "cluster-abc-123" {
						foundInMetricRelabel = true
						break
					}
				}
				g.Expect(foundInMetricRelabel).To(BeTrue(), "cluster ID label should be applied in MetricRelabelConfigs")
			},
		},
		{
			name:       "When metrics set is SRE with no config loaded, MetricRelabelConfigs should only contain cluster ID",
			metricsSet: metrics.MetricsSetSRE,
			clusterID:  "test-cluster",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// KASRelabelConfigs(SRE) returns nil when no SRE config is loaded,
				// so MetricRelabelConfigs only contains the cluster ID entry from ApplyClusterIDLabel.
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs).To(HaveLen(1))
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs[0].TargetLabel).To(Equal("_id"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: tc.clusterID,
				},
			}

			cpContext := component.WorkloadContext{
				Context:    t.Context(),
				HCP:        hcp,
				MetricsSet: tc.metricsSet,
			}

			sm := &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver",
					Namespace: "test-namespace",
				},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Endpoints: []prometheusoperatorv1.Endpoint{
						{
							Port: "client",
						},
					},
				},
			}

			err := adaptServiceMonitor(cpContext, sm)
			tc.validate(t, sm, err)
		})
	}
}
