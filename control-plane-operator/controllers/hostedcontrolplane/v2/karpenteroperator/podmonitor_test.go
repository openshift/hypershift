package karpenteroperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestAdaptPodMonitor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		namespace         string
		clusterID         string
		expectedNamespace string
		expectedClusterID string
		numEndpoints      int
		validateEndpoint  func(t *testing.T, g Gomega, endpoint *prometheusoperatorv1.PodMetricsEndpoint)
	}{
		{
			name:              "When podmonitor is adapted, it should set namespace selector and cluster ID label",
			namespace:         "test-namespace",
			clusterID:         "cluster-12345",
			expectedNamespace: "test-namespace",
			expectedClusterID: "cluster-12345",
			numEndpoints:      1,
			validateEndpoint: func(t *testing.T, g Gomega, endpoint *prometheusoperatorv1.PodMetricsEndpoint) {
				t.Helper()
				g.Expect(endpoint.RelabelConfigs).ToNot(BeNil())
				hasClusterIDLabel := false
				for _, relabelConfig := range endpoint.RelabelConfigs {
					if relabelConfig.TargetLabel == "_id" {
						hasClusterIDLabel = true
						g.Expect(relabelConfig.Replacement).ToNot(BeNil())
						g.Expect(*relabelConfig.Replacement).To(Equal("cluster-12345"))
						break
					}
				}
				g.Expect(hasClusterIDLabel).To(BeTrue(), "Expected cluster ID label to be set")
			},
		},
		{
			name:              "When namespace is different, it should update namespace selector correctly",
			namespace:         "another-namespace",
			clusterID:         "test-cluster",
			expectedNamespace: "another-namespace",
			expectedClusterID: "test-cluster",
			numEndpoints:      1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: tc.clusterID,
				},
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			podMonitor := &prometheusoperatorv1.PodMonitor{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-operator",
					Namespace: "default",
				},
				Spec: prometheusoperatorv1.PodMonitorSpec{
					PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{
						{},
					},
				},
			}

			err := adaptPodMonitor(cpContext, podMonitor)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify namespace selector
			g.Expect(podMonitor.Spec.NamespaceSelector.MatchNames).To(HaveLen(1))
			g.Expect(podMonitor.Spec.NamespaceSelector.MatchNames[0]).To(Equal(tc.expectedNamespace))

			// Verify endpoints
			g.Expect(podMonitor.Spec.PodMetricsEndpoints).To(HaveLen(tc.numEndpoints))

			if tc.validateEndpoint != nil {
				tc.validateEndpoint(t, g, &podMonitor.Spec.PodMetricsEndpoints[0])
			}
		})
	}
}
