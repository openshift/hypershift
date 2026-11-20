package capimanager

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestAdaptPodMonitor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		namespace             string
		clusterID             string
		hcpAnnotations        map[string]string
		removeEndpoints       bool
		expectedAnnotations   map[string]string
		expectedErrorContains string
	}{
		{
			name:      "When HCP has a hosted cluster annotation, it should scope the PodMonitor to the HCP namespace, label series with the cluster ID and set the annotation",
			namespace: "clusters-example",
			clusterID: "6f1d2a4e-3c5b-4f7a-9e8d-1b2c3d4e5f60",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "clusters/example",
			},
			expectedAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "clusters/example",
			},
		},
		{
			name:                "When HCP has no annotations, it should adapt the PodMonitor without setting the hosted cluster annotation",
			namespace:           "clusters-other",
			clusterID:           "0a9b8c7d-6e5f-4a3b-2c1d-0e9f8a7b6c5d",
			expectedAnnotations: nil,
		},
		{
			name:      "When HCP has an empty hosted cluster annotation, it should not set the hosted cluster annotation",
			namespace: "clusters-empty",
			clusterID: "11111111-2222-4333-8444-555555555555",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "",
			},
			expectedAnnotations: nil,
		},
		{
			name:                  "When the PodMonitor has no pod metrics endpoints, it should return an error",
			namespace:             "clusters-example",
			clusterID:             "6f1d2a4e-3c5b-4f7a-9e8d-1b2c3d4e5f60",
			removeEndpoints:       true,
			expectedErrorContains: "has no pod metrics endpoints",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			obj, _, err := assets.LoadManifest(ComponentName, "podmonitor.yaml")
			g.Expect(err).ToNot(HaveOccurred())
			podMonitor, ok := obj.(*prometheusoperatorv1.PodMonitor)
			g.Expect(ok).To(BeTrue(), "expected embedded podmonitor.yaml to decode into a PodMonitor, got %T", obj)

			// The embedded asset defines what is scraped: the metrics port of the cluster-api pods over HTTP.
			g.Expect(podMonitor.Name).To(Equal("cluster-api"))
			g.Expect(podMonitor.Spec.Selector.MatchLabels).To(Equal(map[string]string{"app": "cluster-api"}))
			g.Expect(podMonitor.Spec.PodMetricsEndpoints).To(HaveLen(1))
			endpoint := podMonitor.Spec.PodMetricsEndpoints[0]
			g.Expect(endpoint.Port).To(Equal(ptr.To("metrics")))
			g.Expect(endpoint.Path).To(Equal("/metrics"))
			g.Expect(endpoint.Interval).To(Equal(prometheusoperatorv1.Duration("30s")))
			g.Expect(endpoint.Scheme).To(Equal(ptr.To(prometheusoperatorv1.Scheme("http"))))

			if tc.removeEndpoints {
				podMonitor.Spec.PodMetricsEndpoints = nil
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "hcp",
						Namespace:   tc.namespace,
						Annotations: tc.hcpAnnotations,
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						ClusterID: tc.clusterID,
					},
				},
			}

			err = adaptPodMonitor(cpContext, podMonitor)
			if tc.expectedErrorContains != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorContains))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(podMonitor.Spec.NamespaceSelector).To(Equal(prometheusoperatorv1.NamespaceSelector{MatchNames: []string{tc.namespace}}))

			expectedClusterIDRelabel := prometheusoperatorv1.RelabelConfig{
				TargetLabel: "_id",
				Action:      "replace",
				Replacement: ptr.To(tc.clusterID),
			}
			adaptedEndpoint := podMonitor.Spec.PodMetricsEndpoints[0]
			g.Expect(adaptedEndpoint.RelabelConfigs).To(ConsistOf(expectedClusterIDRelabel))
			g.Expect(adaptedEndpoint.MetricRelabelConfigs).To(ConsistOf(expectedClusterIDRelabel))

			// Adapting must not change the scrape target defined by the asset.
			g.Expect(adaptedEndpoint.Port).To(Equal(ptr.To("metrics")))
			g.Expect(adaptedEndpoint.Path).To(Equal("/metrics"))
			g.Expect(adaptedEndpoint.Scheme).To(Equal(ptr.To(prometheusoperatorv1.Scheme("http"))))

			if tc.expectedAnnotations == nil {
				g.Expect(podMonitor.Annotations).ToNot(HaveKey(k8sutil.HostedClusterAnnotation))
			} else {
				g.Expect(podMonitor.Annotations).To(Equal(tc.expectedAnnotations))
			}
		})
	}
}
