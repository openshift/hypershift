package olmoperator

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

	testCases := []struct {
		name               string
		hcpNamespace       string
		smNamespace        string
		clusterID          string
		metricsSet         metrics.MetricsSet
		expectedNamespaces []string
	}{
		{
			name:               "When ServiceMonitor is adapted, it should configure namespace selector and metric relabel configs",
			hcpNamespace:       "hcp-namespace",
			smNamespace:        "test-namespace",
			clusterID:          "test-cluster-id",
			metricsSet:         metrics.MetricsSetTelemetry,
			expectedNamespaces: []string{"test-namespace"},
		},
		{
			name:               "When ServiceMonitor has different namespace, it should use that namespace",
			hcpNamespace:       "hcp-namespace",
			smNamespace:        "another-namespace",
			clusterID:          "another-cluster-id",
			metricsSet:         metrics.MetricsSetAll,
			expectedNamespaces: []string{"another-namespace"},
		},
		{
			name:               "When metrics set is SRE, it should still configure namespace selector with SM namespace",
			hcpNamespace:       "sre-hcp-namespace",
			smNamespace:        "sre-namespace",
			clusterID:          "sre-cluster-id",
			metricsSet:         metrics.MetricsSetSRE,
			expectedNamespaces: []string{"sre-namespace"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: tc.clusterID,
				},
			}

			cpContext := component.WorkloadContext{
				HCP:        hcp,
				MetricsSet: tc.metricsSet,
			}

			sm := &prometheusoperatorv1.ServiceMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sm",
					Namespace: tc.smNamespace,
				},
				Spec: prometheusoperatorv1.ServiceMonitorSpec{
					Endpoints: []prometheusoperatorv1.Endpoint{
						{},
					},
				},
			}

			err := adaptServiceMonitor(cpContext, sm)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify namespace selector
			g.Expect(sm.Spec.NamespaceSelector.MatchNames).To(Equal(tc.expectedNamespaces))

			// Verify metric relabel configs are set (not checking exact equality since they include cluster ID)
			g.Expect(len(sm.Spec.Endpoints[0].MetricRelabelConfigs)).To(BeNumerically(">", 0))

			// Verify cluster ID label is in metric relabel configs
			var clusterIDLabelFound bool
			for _, relabelConfig := range sm.Spec.Endpoints[0].MetricRelabelConfigs {
				if relabelConfig.TargetLabel == "_id" {
					g.Expect(relabelConfig.Replacement).ToNot(BeNil())
					g.Expect(*relabelConfig.Replacement).To(Equal(tc.clusterID))
					clusterIDLabelFound = true
					break
				}
			}
			g.Expect(clusterIDLabelFound).To(BeTrue(), "cluster ID label should be in metric relabel configs")
		})
	}
}
