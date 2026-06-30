package oapi

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptForExternalOIDC(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		validate func(*testing.T, *GomegaWithT, *hyperv1.HostedControlPlane)
	}{
		{
			name: "When adapted, it should use external-oidc subcommand",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args[0]).To(Equal("external-oidc"))
				g.Expect(container.Args).To(ContainElement("--config=/etc/kubernetes/config/auth-config/auth-config.json"))
				g.Expect(container.Args).To(ContainElement("--secure-port=8443"))
				g.Expect(container.Args).To(ContainElement("--tls-private-key-file=/etc/kubernetes/certs/serving/tls.key"))
				g.Expect(container.Args).To(ContainElement("--tls-cert-file=/etc/kubernetes/certs/serving/tls.crt"))
				g.Expect(container.Args).To(ContainElement("--v=2"))
			},
		},
		{
			name: "When adapted, it should add auth-config volume and mount",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				volume := podspec.FindVolume("auth-config", deployment.Spec.Template.Spec.Volumes)
				g.Expect(volume).ToNot(BeNil())
				g.Expect(volume.VolumeSource.ConfigMap).ToNot(BeNil())
				g.Expect(volume.VolumeSource.ConfigMap.Name).To(Equal("openshift-oauth-apiserver-auth-config"))

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				volumeMount := podspec.FindVolumeMount("auth-config", container.VolumeMounts)
				g.Expect(volumeMount).ToNot(BeNil())
				g.Expect(volumeMount.MountPath).To(Equal("/etc/kubernetes/config/auth-config"))
			},
		},
		{
			name: "When TLS security profile is Modern, it should configure tls-min-version",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type:   configv1.TLSProfileModernType,
								Modern: &configv1.ModernTLSProfile{},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--tls-min-version=VersionTLS13"))
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--tls-cipher-suites=")))
			},
		},
		{
			name: "When TLS security profile is Intermediate, it should configure tls-cipher-suites",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type:         configv1.TLSProfileIntermediateType,
								Intermediate: &configv1.IntermediateTLSProfile{},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--tls-min-version=VersionTLS12"))
				g.Expect(container.Args).To(ContainElement(ContainSubstring("--tls-cipher-suites=")))
			},
		},
		{
			name: "When adapted, it should set NO_PROXY to kube-apiserver only",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				noProxyEnv := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxyEnv).ToNot(BeNil())
				g.Expect(noProxyEnv.Value).To(Equal("kube-apiserver"))
			},
		},
		{
			name: "When adapted, it should not add OAuth-specific resources",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptForExternalOIDC(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--etcd")))
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--kubeconfig")))
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--audit-log")))
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--requestheader")))
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--client-ca-file")))

				g.Expect(podspec.FindContainer("audit-logs", deployment.Spec.Template.Spec.Containers)).To(BeNil())
				g.Expect(podspec.FindContainer("kas-readiness-check", deployment.Spec.Template.Spec.Containers)).To(BeNil())

				g.Expect(podspec.FindVolume("etcd-client-cert", deployment.Spec.Template.Spec.Volumes)).To(BeNil())
				g.Expect(podspec.FindVolume("kubeconfig", deployment.Spec.Template.Spec.Volumes)).To(BeNil())
				g.Expect(podspec.FindVolume("work-logs", deployment.Spec.Template.Spec.Volumes)).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			tc.validate(t, g, tc.hcp)
		})
	}
}
