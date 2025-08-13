package kas

import (
	"os"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/config"

	routev1 "github.com/openshift/api/route/v1"
)

func TestGetHealthcheckEndpoint(t *testing.T) {
	tests := []struct {
		name             string
		route            *routev1.Route
		hcp              *hyperv1.HostedControlPlane
		useSharedIngress bool
		expectedEndpoint string
		expectedPort     int
		expectedError    string
	}{
		{
			name: "when route is not admitted, it should return error",
			route: &routev1.Route{
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{},
				},
			},
			hcp:           &hyperv1.HostedControlPlane{},
			expectedError: "APIServer external route not admitted",
		},
		{
			name: "when route is admitted without shared ingress, it should return canonical hostname",
			route: &routev1.Route{
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "test.example.com",
						},
					},
				},
			},
			hcp:              &hyperv1.HostedControlPlane{},
			useSharedIngress: false,
			expectedEndpoint: "test.example.com",
			expectedPort:     443,
		},
		{
			name: "when using shared ingress, it should return route host",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Host: "route.example.com",
				},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "test.example.com",
						},
					},
				},
			},
			hcp:              &hyperv1.HostedControlPlane{},
			useSharedIngress: true,
			expectedEndpoint: "route.example.com",
			expectedPort:     sharedingress.ExternalDNSLBPort,
		},
		{
			name: "when using shared ingress with CIDR blocks, it should return KAS service",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Host: "route.example.com",
				},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterCanonicalHostname: "test.example.com",
						},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							AllowedCIDRBlocks: []hyperv1.CIDRBlock{"10.0.0.0/16"},
						},
					},
				},
			},
			useSharedIngress: true,
			expectedEndpoint: manifests.KubeAPIServerService("").Name,
			expectedPort:     config.KASSVCPort,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.useSharedIngress {
				os.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
				defer os.Unsetenv("MANAGED_SERVICE")
			}

			endpoint, port, err := GetHealthcheckEndpointForRoute(tc.route, tc.hcp)

			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(endpoint).To(Equal(tc.expectedEndpoint))
				g.Expect(port).To(Equal(tc.expectedPort))
			}
		})
	}
}
