package globalconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileIngressConfig(t *testing.T) {
	testsCases := []struct {
		name                  string
		inputHCP              *hyperv1.HostedControlPlane
		inputIngressConfig    *configv1.Ingress
		expectedIngressConfig *configv1.Ingress
	}{
		{
			name:               "When no configuration is specified it should set the default domain",
			inputIngressConfig: IngressConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
				},
			},
		},
		{
			name:               "When ingress configuration is specified it should be copied to the Ingress object",
			inputIngressConfig: IngressConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "custom.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "custom.example.com",
				},
			},
		},
		{
			name: "When guest cluster has componentRoutes it should preserve them after reconciliation",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "custom-console.example.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "custom-console-cert",
							},
						},
					},
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "custom-console.example.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "custom-console-cert",
							},
						},
					},
				},
			},
		},
		{
			name: "When guest cluster has componentRoutes and HCP spec also has componentRoutes it should preserve the guest cluster ones",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "guest-console.example.com",
						},
					},
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
							ComponentRoutes: []configv1.ComponentRouteSpec{
								{
									Namespace: "openshift-console",
									Name:      "console",
									Hostname:  "hcp-console.example.com",
								},
							},
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "guest-console.example.com",
						},
					},
				},
			},
		},
		{
			name: "When guest cluster has multiple componentRoutes it should preserve all of them",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "console.custom.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "console-cert",
							},
						},
						{
							Namespace: "openshift-console",
							Name:      "downloads",
							Hostname:  "downloads.custom.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "downloads-cert",
							},
						},
					},
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "console.custom.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "console-cert",
							},
						},
						{
							Namespace: "openshift-console",
							Name:      "downloads",
							Hostname:  "downloads.custom.com",
							ServingCertKeyPairSecret: configv1.SecretNameReference{
								Name: "downloads-cert",
							},
						},
					},
				},
			},
		},
		{
			name:               "When guest cluster has no componentRoutes and HCP has none it should leave componentRoutes nil",
			inputIngressConfig: IngressConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
				},
			},
		},
		{
			name:               "When guest cluster has no componentRoutes but HCP has them it should not populate componentRoutes",
			inputIngressConfig: IngressConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
							ComponentRoutes: []configv1.ComponentRouteSpec{
								{
									Namespace: "openshift-console",
									Name:      "console",
									Hostname:  "hcp-console.example.com",
								},
							},
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
				},
			},
		},
		{
			name:               "When guest cluster has no appsDomain but HCP has one it should not populate appsDomain",
			inputIngressConfig: IngressConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain:     "apps.cluster.example.com",
							AppsDomain: "hcp-apps.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain: "apps.cluster.example.com",
				},
			},
		},
		{
			name: "When guest cluster has appsDomain it should preserve it after reconciliation",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "custom-apps.example.com",
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "custom-apps.example.com",
				},
			},
		},
		{
			name: "When guest cluster has appsDomain and HCP spec also has appsDomain it should preserve the guest cluster one",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "guest-apps.example.com",
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain:     "apps.cluster.example.com",
							AppsDomain: "hcp-apps.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "guest-apps.example.com",
				},
			},
		},
		{
			name: "When guest cluster has both appsDomain and componentRoutes it should preserve both",
			inputIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "custom-apps.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "console.custom.com",
						},
					},
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Ingress: &configv1.IngressSpec{
							Domain: "apps.cluster.example.com",
						},
					},
				},
			},
			expectedIngressConfig: &configv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.IngressSpec{
					Domain:     "apps.cluster.example.com",
					AppsDomain: "custom-apps.example.com",
					ComponentRoutes: []configv1.ComponentRouteSpec{
						{
							Namespace: "openshift-console",
							Name:      "console",
							Hostname:  "console.custom.com",
						},
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ReconcileIngressConfig(tc.inputIngressConfig, tc.inputHCP)
			g.Expect(tc.inputIngressConfig).To(BeEquivalentTo(tc.expectedIngressConfig))
		})
	}
}
