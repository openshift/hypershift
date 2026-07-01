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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetHealthcheckEndpoint(t *testing.T) {
	tests := []struct {
		name                    string
		route                   *routev1.Route
		hcp                     *hyperv1.HostedControlPlane
		useSharedIngress        bool
		expectedEndpoint        string
		expectedPort            int
		expectedErrorSubstrings []string
	}{
		{
			name: "When route has no ingress status, it should return error with no ingress detail",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver",
					Namespace: "clusters-test",
				},
				Spec: routev1.RouteSpec{
					Host: "api.test.example.com",
				},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{},
				},
			},
			hcp: &hyperv1.HostedControlPlane{},
			expectedErrorSubstrings: []string{
				"clusters-test/kube-apiserver",
				"api.test.example.com",
				"route has no ingress status",
			},
		},
		{
			name: "When route has ingress but no canonical hostname, it should return error with router diagnostic",
			route: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver",
					Namespace: "clusters-test",
				},
				Spec: routev1.RouteSpec{
					Host: "api.test.example.com",
				},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{
						{
							RouterName:              "default",
							RouterCanonicalHostname: "",
							Conditions: []routev1.RouteIngressCondition{
								{
									Type:    routev1.RouteAdmitted,
									Status:  corev1.ConditionFalse,
									Reason:  "HostAlreadyClaimed",
									Message: "route foo already exposes api.test.example.com",
								},
							},
						},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{},
			expectedErrorSubstrings: []string{
				"clusters-test/kube-apiserver",
				"api.test.example.com",
				"not admitted",
				"default",
				"has not set a canonical hostname",
				"Admitted=False",
				"HostAlreadyClaimed",
				"route foo already exposes",
			},
		},
		{
			name: "When route is admitted without shared ingress, it should return canonical hostname",
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
			name: "When using shared ingress, it should return route host",
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
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Private: hyperv1.AzurePrivateSpec{
								Type: hyperv1.AzurePrivateTypeSwift,
								Swift: hyperv1.AzureSwiftSpec{
									PodNetworkInstance: "test-pni",
								},
							},
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
							},
						},
					},
				},
			},
			useSharedIngress: true,
			expectedEndpoint: "route.example.com",
			expectedPort:     sharedingress.ExternalDNSLBPort,
		},
		{
			name: "When using shared ingress with CIDR blocks, it should return KAS service",
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
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Private: hyperv1.AzurePrivateSpec{
								Type: hyperv1.AzurePrivateTypeSwift,
								Swift: hyperv1.AzureSwiftSpec{
									PodNetworkInstance: "test-pni",
								},
							},
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
							},
						},
					},
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

			if len(tc.expectedErrorSubstrings) > 0 {
				g.Expect(err).To(HaveOccurred())
				for _, substr := range tc.expectedErrorSubstrings {
					g.Expect(err.Error()).To(ContainSubstring(substr))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(endpoint).To(Equal(tc.expectedEndpoint))
				g.Expect(port).To(Equal(tc.expectedPort))
			}
		})
	}
}
