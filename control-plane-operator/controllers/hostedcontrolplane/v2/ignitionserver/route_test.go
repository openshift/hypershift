package ignitionserver

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIgnitionRouteAdapt(t *testing.T) {
	testCases := []struct {
		name                string
		hcp                 *hyperv1.HostedControlPlane
		route               *routev1.Route
		expectedHost        string
		expectHCPRouteLabel bool
		expectInternalLabel bool
	}{
		{
			name: "When HCP is private it should use the internal hostname and label the route as internal",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
						},
					},
				},
			},
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-internal",
					Namespace: "test-ns",
				},
			},
			expectedHost:        "ignition-server.apps.test-hcp.hypershift.local",
			expectHCPRouteLabel: true,
			expectInternalLabel: true,
		},
		{
			name: "When HCP is public it should use the strategy hostname and avoid the internal label",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Public,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "ignition.example.com",
								},
							},
						},
					},
				},
			},
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server",
					Namespace: "test-ns",
				},
			},
			expectedHost:        "ignition.example.com",
			expectHCPRouteLabel: false,
			expectInternalLabel: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ign := &ignitionServer{defaultIngressDomain: "example.com"}
			cpContext := component.WorkloadContext{HCP: tc.hcp}

			err := ign.adaptRoute(cpContext, tc.route)
			g.Expect(err).To(BeNil())
			g.Expect(tc.route.Spec.Host).To(Equal(tc.expectedHost))

			if tc.expectHCPRouteLabel {
				g.Expect(tc.route.Labels).To(HaveKeyWithValue(util.HCPRouteLabel, tc.route.Namespace))
			} else {
				g.Expect(tc.route.Labels).ToNot(HaveKeyWithValue(util.HCPRouteLabel, tc.route.Namespace))
			}

			if tc.expectInternalLabel {
				g.Expect(tc.route.Labels).To(HaveKeyWithValue(util.InternalRouteLabel, "true"))
			} else {
				g.Expect(tc.route.Labels).ToNot(HaveKey(util.InternalRouteLabel))
			}
		})
	}
}
