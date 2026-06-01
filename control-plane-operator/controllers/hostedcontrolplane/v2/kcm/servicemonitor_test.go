package kcm

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
			name:       "When service monitor is adapted, it should apply metric relabel configs",
			metricsSet: metrics.MetricsSetTelemetry,
			clusterID:  "test-cluster-id",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// MetricRelabelConfigs should be set based on metrics set
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs).ToNot(BeNil())
			},
		},
		{
			name:       "When service monitor is adapted, it should apply cluster ID label",
			metricsSet: metrics.MetricsSetAll,
			clusterID:  "cluster-abc-123",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// Check that cluster ID label is applied
				relabelConfigs := sm.Spec.Endpoints[0].RelabelConfigs
				foundClusterIDLabel := false
				for _, config := range relabelConfigs {
					if config.TargetLabel == "_id" && config.Replacement != nil && *config.Replacement == "cluster-abc-123" {
						foundClusterIDLabel = true
						break
					}
				}
				g.Expect(foundClusterIDLabel).To(BeTrue(), "cluster ID label should be applied")
			},
		},
		{
			name:       "When metrics set is SRE, it should apply SRE configs",
			metricsSet: metrics.MetricsSetSRE,
			clusterID:  "test-cluster",
			validate: func(t *testing.T, sm *prometheusoperatorv1.ServiceMonitor, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sm.Spec.Endpoints).To(HaveLen(1))

				// MetricRelabelConfigs should be set based on metrics set
				g.Expect(sm.Spec.Endpoints[0].MetricRelabelConfigs).ToNot(BeNil())
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
					Name:      "kube-controller-manager",
					Namespace: "test-namespace",
				},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Endpoints: []prometheusoperatorv1.Endpoint{
						{
							Port: "metrics",
						},
					},
				},
			}

			err := adaptServiceMonitor(cpContext, sm)
			tc.validate(t, sm, err)
		})
	}
}
