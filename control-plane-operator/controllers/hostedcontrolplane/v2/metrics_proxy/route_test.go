package metricsproxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptRoute(t *testing.T) {
	t.Run("When ignition strategy has an explicit hostname it should derive the metrics-proxy hostname", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mp := &metricsProxy{defaultIngressDomain: "apps.example.com"}
		route := &routev1.Route{}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters-test",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
				},
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.Ignition,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: "ignition-test-cluster.custom.domain.com",
							},
						},
					},
				},
			},
		}
		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		err := mp.adaptRoute(cpContext, route)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(route.Spec.Host).To(Equal("metrics-proxy-test-cluster.custom.domain.com"))
	})

	t.Run("When ignition strategy has no explicit hostname it should use the default ingress domain", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mp := &metricsProxy{defaultIngressDomain: "apps.example.com"}
		route := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "metrics-proxy",
				Namespace: "clusters-very-long-hosted-cluster-name-that-exceeds-the-limit",
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters-test",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				Services: []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.Ignition,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						},
					},
				},
			},
		}
		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		err := mp.adaptRoute(cpContext, route)
		g.Expect(err).ToNot(HaveOccurred())
		// With no explicit hostname, ReconcileExternalRoute uses the default ingress domain.
		g.Expect(route.Spec.Host).To(ContainSubstring("apps.example.com"))
	})

	t.Run("When there is no ignition service strategy it should not derive a hostname", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mp := &metricsProxy{defaultIngressDomain: "apps.example.com"}
		route := &routev1.Route{}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters-test",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				Services: []hyperv1.ServicePublishingStrategyMapping{},
			},
		}
		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		err := mp.adaptRoute(cpContext, route)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(route.Spec.Host).To(BeEmpty())
	})

	t.Run("When the HCP is private it should reconcile an internal route", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mp := &metricsProxy{defaultIngressDomain: "apps.example.com"}
		route := &routev1.Route{}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters-test",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						EndpointAccess: hyperv1.Private,
					},
				},
			},
		}
		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		err := mp.adaptRoute(cpContext, route)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(route.Spec.Host).To(ContainSubstring(".hypershift.local"))
	})
}
