package oapi

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		validate func(*testing.T, *GomegaWithT, *hyperv1.HostedControlPlane)
	}{
		{
			name: "When etcd is managed, it should configure default etcd URL",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--etcd-servers=https://etcd-client:2379"))
				g.Expect(container.Args).To(ContainElement("--api-audiences=https://test-issuer.example.com"))
			},
		},
		{
			name: "When etcd is unmanaged, it should configure custom etcd endpoint",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Unmanaged,
						Unmanaged: &hyperv1.UnmanagedEtcdSpec{
							Endpoint: "https://custom-etcd.example.com:2379",
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--etcd-servers=https://custom-etcd.example.com:2379"))

				// Check NO_PROXY includes the custom etcd hostname
				noProxyEnv := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxyEnv).ToNot(BeNil())
				g.Expect(noProxyEnv.Value).To(ContainSubstring("custom-etcd.example.com"))
			},
		},
		{
			name: "When TLS security profile is set, it should configure tls-min-version",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--tls-min-version=VersionTLS13"))
			},
		},
		{
			name: "When audit webhook is configured, it should add audit webhook arguments and volume",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					AuditWebhook: &corev1.LocalObjectReference{
						Name: "audit-webhook-secret",
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--audit-webhook-config-file=/etc/kubernetes/auditwebhook/webhook-kubeconfig"))
				g.Expect(container.Args).To(ContainElement("--audit-webhook-mode=batch"))
				g.Expect(container.Args).To(ContainElement("--audit-webhook-initial-backoff=5s"))

				// Check volume mount
				volumeMount := podspec.FindVolumeMount(auditWebhookConfigFileVolumeName, container.VolumeMounts)
				g.Expect(volumeMount).ToNot(BeNil())
				g.Expect(volumeMount.MountPath).To(Equal("/etc/kubernetes/auditwebhook"))

				// Check volume
				volume := podspec.FindVolume(auditWebhookConfigFileVolumeName, deployment.Spec.Template.Spec.Volumes)
				g.Expect(volume).ToNot(BeNil())
				g.Expect(volume.VolumeSource.Secret).ToNot(BeNil())
				g.Expect(volume.VolumeSource.Secret.SecretName).To(Equal("audit-webhook-secret"))
			},
		},
		{
			name: "When audit webhook is not configured, it should not add audit webhook arguments",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--audit-webhook-config-file")))
				g.Expect(container.Args).ToNot(ContainElement("--audit-webhook-mode=batch"))

				// Check volume mount doesn't exist
				volumeMount := podspec.FindVolumeMount(auditWebhookConfigFileVolumeName, container.VolumeMounts)
				g.Expect(volumeMount).To(BeNil())

				// Check volume doesn't exist
				volume := podspec.FindVolume(auditWebhookConfigFileVolumeName, deployment.Spec.Template.Spec.Volumes)
				g.Expect(volume).To(BeNil())
			},
		},
		{
			name: "When access token inactivity timeout is configured, it should add timeout argument",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							TokenConfig: configv1.TokenConfig{
								AccessTokenInactivityTimeout: &metav1.Duration{
									Duration: 10 * time.Minute,
								},
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--accesstoken-inactivity-timeout=10m0s"))
			},
		},
		{
			name: "When access token inactivity timeout is not configured, it should not add timeout argument",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							TokenConfig: configv1.TokenConfig{},
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--accesstoken-inactivity-timeout")))
			},
		},
		{
			name: "When audit profile is None, it should remove audit-logs container",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							Audit: configv1.Audit{
								Profile: configv1.NoneAuditProfileType,
							},
						},
					},
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				// Verify audit-logs container exists in base manifest before adaptation
				preContainer := podspec.FindContainer("audit-logs", deployment.Spec.Template.Spec.Containers)
				g.Expect(preContainer).ToNot(BeNil(), "audit-logs container should exist in base manifest")

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer("audit-logs", deployment.Spec.Template.Spec.Containers)
				g.Expect(container).To(BeNil(), "audit-logs container should be removed after adaptation")
			},
		},
		{
			name: "When NO_PROXY is set, it should include kube-apiserver and etcd",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				noProxyEnv := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxyEnv).ToNot(BeNil())
				g.Expect(noProxyEnv.Value).To(ContainSubstring("kube-apiserver"))
				g.Expect(noProxyEnv.Value).To(ContainSubstring("etcd-client"))
			},
		},
		{
			name: "When deployment is adapted, it should add KAS readiness check container",
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
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			validate: func(t *testing.T, g *GomegaWithT, hcp *hyperv1.HostedControlPlane) {

				deployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())

				originalContainerCount := len(deployment.Spec.Template.Spec.Containers)

				cpContext := component.WorkloadContext{
					Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
					HCP:    hcp,
				}

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				// KAS readiness check container should be added
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(originalContainerCount + 1))
				kasReadinessContainer := podspec.FindContainer("kas-readiness-check", deployment.Spec.Template.Spec.Containers)
				g.Expect(kasReadinessContainer).ToNot(BeNil(), "kas-readiness-check container should be present")
			},
		},
		{
			name: "When platform is Azure, it should use correct KAS URL",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					IssuerURL: "https://test-issuer.example.com",
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
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

				err := adaptDeployment(cpContext, deployment)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify KAS readiness check container was added with /livez URL
				kasReadinessContainer := podspec.FindContainer("kas-readiness-check", deployment.Spec.Template.Spec.Containers)
				g.Expect(kasReadinessContainer).ToNot(BeNil(), "KAS readiness check container should be present")
				g.Expect(kasReadinessContainer.ReadinessProbe).ToNot(BeNil(), "KAS readiness check container should have a readiness probe")
				g.Expect(kasReadinessContainer.ReadinessProbe.Exec).ToNot(BeNil(), "readiness probe should use exec")
				g.Expect(kasReadinessContainer.ReadinessProbe.Exec.Command).To(ContainElement(ContainSubstring("/livez")))
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

func TestApplyAuditWebhookConfigFileVolume(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		auditWebhookRef   *corev1.LocalObjectReference
		expectedVolume    *corev1.Volume
		expectedMountPath string
	}{
		{
			name: "When audit webhook ref is provided, it should add volume and mount",
			auditWebhookRef: &corev1.LocalObjectReference{
				Name: "test-audit-webhook",
			},
			expectedVolume: &corev1.Volume{
				Name: auditWebhookConfigFileVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "test-audit-webhook",
					},
				},
			},
			expectedMountPath: "/etc/kubernetes/auditwebhook",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			applyAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, tc.auditWebhookRef)

			// Check volume was added
			volume := podspec.FindVolume(auditWebhookConfigFileVolumeName, deployment.Spec.Template.Spec.Volumes)
			g.Expect(volume).ToNot(BeNil())
			g.Expect(volume.Name).To(Equal(tc.expectedVolume.Name))
			g.Expect(volume.VolumeSource.Secret).ToNot(BeNil())
			g.Expect(volume.VolumeSource.Secret.SecretName).To(Equal(tc.expectedVolume.VolumeSource.Secret.SecretName))

			// Check volume mount was added to the component container
			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())

			volumeMount := podspec.FindVolumeMount(auditWebhookConfigFileVolumeName, container.VolumeMounts)
			g.Expect(volumeMount).ToNot(BeNil())
			g.Expect(volumeMount.MountPath).To(Equal(tc.expectedMountPath))
		})
	}
}

func TestAdaptDeploymentWithInvalidEtcdURL(t *testing.T) {
	t.Parallel()

	t.Run("When unmanaged etcd endpoint is invalid, it should return error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-ns",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				IssuerURL: "https://test-issuer.example.com",
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Unmanaged,
					Unmanaged: &hyperv1.UnmanagedEtcdSpec{
						Endpoint: "://invalid-url",
					},
				},
			},
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		cpContext := component.WorkloadContext{
			Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			HCP:    hcp,
		}

		err = adaptDeployment(cpContext, deployment)
		g.Expect(err).To(HaveOccurred())
	})
}

func TestAdaptDeploymentMultipleConfigurations(t *testing.T) {
	t.Parallel()

	t.Run("When multiple configurations are set, it should apply all of them", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-ns",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				IssuerURL: "https://test-issuer.example.com",
				Etcd: hyperv1.EtcdSpec{
					ManagementType: hyperv1.Unmanaged,
					Unmanaged: &hyperv1.UnmanagedEtcdSpec{
						Endpoint: "https://custom-etcd.example.com:2379",
					},
				},
				AuditWebhook: &corev1.LocalObjectReference{
					Name: "audit-webhook-secret",
				},
				Configuration: &hyperv1.ClusterConfiguration{
					OAuth: &configv1.OAuthSpec{
						TokenConfig: configv1.TokenConfig{
							AccessTokenInactivityTimeout: &metav1.Duration{
								Duration: 15 * time.Minute,
							},
						},
					},
					APIServer: &configv1.APIServerSpec{
						TLSSecurityProfile: &configv1.TLSSecurityProfile{
							Type:   configv1.TLSProfileModernType,
							Modern: &configv1.ModernTLSProfile{},
						},
					},
				},
			},
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		cpContext := component.WorkloadContext{
			Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			HCP:    hcp,
		}

		err = adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
		g.Expect(container).ToNot(BeNil())

		// Check all configurations are applied
		g.Expect(container.Args).To(ContainElement("--etcd-servers=https://custom-etcd.example.com:2379"))
		g.Expect(container.Args).To(ContainElement("--audit-webhook-config-file=/etc/kubernetes/auditwebhook/webhook-kubeconfig"))
		g.Expect(container.Args).To(ContainElement("--accesstoken-inactivity-timeout=15m0s"))
		g.Expect(container.Args).To(ContainElement("--tls-min-version=VersionTLS13"))

		// Check volume and mount
		volume := podspec.FindVolume(auditWebhookConfigFileVolumeName, deployment.Spec.Template.Spec.Volumes)
		g.Expect(volume).ToNot(BeNil())

		volumeMount := podspec.FindVolumeMount(auditWebhookConfigFileVolumeName, container.VolumeMounts)
		g.Expect(volumeMount).ToNot(BeNil())

		// Check NO_PROXY
		noProxyEnv := podspec.FindEnvVar("NO_PROXY", container.Env)
		g.Expect(noProxyEnv).ToNot(BeNil())
		g.Expect(noProxyEnv.Value).To(ContainSubstring("custom-etcd.example.com"))
	})
}
