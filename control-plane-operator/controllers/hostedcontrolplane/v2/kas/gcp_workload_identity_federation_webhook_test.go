package kas

import (
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyGCPWorkloadIdentityFederationWebhookContainer(t *testing.T) {
	testCases := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		validatePod func(*GomegaWithT, *corev1.PodSpec)
	}{
		{
			name: "When applying the GCP webhook container, it should add the sidecar with correct command flags",
			hcp:  testGCPHostedControlPlane(nil),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Image).To(Equal("gcp-workload-identity-federation-webhook"))
				g.Expect(webhookContainer.Command).To(ContainElement("/usr/bin/gcp-workload-identity-federation-webhook"))
				g.Expect(webhookContainer.Command).To(ContainElement("--annotation-prefix=cloud.google.com"))
				g.Expect(webhookContainer.Command).To(ContainElement("--gcp-default-region=us-central1"))
				g.Expect(webhookContainer.Command).To(ContainElement("--kubeconfig=/var/run/app/kubeconfig/kubeconfig"))
				g.Expect(webhookContainer.Command).To(ContainElement("--token-audience=sts.googleapis.com"))
			},
		},
		{
			name: "When applying the GCP webhook container, it should configure health probes",
			hcp:  testGCPHostedControlPlane(nil),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())

				g.Expect(webhookContainer.StartupProbe).NotTo(BeNil())
				g.Expect(webhookContainer.StartupProbe.HTTPGet.Path).To(Equal("/healthz"))
				g.Expect(webhookContainer.StartupProbe.HTTPGet.Port.IntValue()).To(Equal(8081))
				g.Expect(webhookContainer.StartupProbe.FailureThreshold).To(Equal(int32(30)))

				g.Expect(webhookContainer.LivenessProbe).NotTo(BeNil())
				g.Expect(webhookContainer.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
				g.Expect(webhookContainer.LivenessProbe.HTTPGet.Port.IntValue()).To(Equal(8081))

				g.Expect(webhookContainer.ReadinessProbe).NotTo(BeNil())
				g.Expect(webhookContainer.ReadinessProbe.HTTPGet.Path).To(Equal("/readyz"))
				g.Expect(webhookContainer.ReadinessProbe.HTTPGet.Port.IntValue()).To(Equal(8081))
			},
		},
		{
			name: "When applying the GCP webhook container, it should add serving cert and kubeconfig volumes",
			hcp:  testGCPHostedControlPlane(nil),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				volumeNames := make(map[string]string)
				for _, v := range podSpec.Volumes {
					if v.Secret != nil {
						volumeNames[v.Name] = v.Secret.SecretName
					}
				}
				g.Expect(volumeNames).To(HaveKeyWithValue(
					gcpWorkloadIdentityFederationWebhookServingCertVolumeName,
					manifests.GCPWorkloadIdentityFederationWebhookServingCert("").Name,
				))
				g.Expect(volumeNames).To(HaveKeyWithValue(
					gcpWorkloadIdentityFederationWebhookKubeconfigVolumeName,
					manifests.GCPWorkloadIdentityFederationWebhookKubeconfig("").Name,
				))

				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				mountPaths := make(map[string]string)
				for _, vm := range webhookContainer.VolumeMounts {
					mountPaths[vm.Name] = vm.MountPath
				}
				g.Expect(mountPaths).To(HaveKeyWithValue(gcpWorkloadIdentityFederationWebhookServingCertVolumeName, "/tmp/k8s-webhook-server/serving-certs"))
				g.Expect(mountPaths).To(HaveKeyWithValue(gcpWorkloadIdentityFederationWebhookKubeconfigVolumeName, "/var/run/app/kubeconfig"))
			},
		},
		{
			name: "When applying the GCP webhook container with a modern TLS profile, it should add TLS 1.3 configuration",
			hcp: testGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS13"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeFalse())
			},
		},
		{
			name: "When applying the GCP webhook container with an old TLS profile, it should add TLS 1.0 configuration",
			hcp: testGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			}),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS10"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeTrue())
			},
		},
		{
			name: "When applying the GCP webhook container with an intermediate TLS profile, it should use webhook TLS cipher flag",
			hcp: testGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			}),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeTrue())
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--cipher-suites=")
				})).To(BeFalse())
			},
		},
		{
			name: "When applying the GCP webhook container with a custom TLS profile, it should add custom TLS configuration",
			hcp: testGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS11,
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
						},
					},
				},
			}),
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "gcp-workload-identity-federation-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS11"))
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			podSpec := &corev1.PodSpec{}
			err := applyGCPWorkloadIdentityFederationWebhookContainer(podSpec, tc.hcp)
			g.Expect(err).ToNot(HaveOccurred())
			tc.validatePod(g, podSpec)
		})
	}
}

func TestApplyGCPWorkloadIdentityFederationWebhookContainerError(t *testing.T) {
	t.Run("When GCP platform details are missing, it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		hcp := testGCPHostedControlPlane(nil)
		hcp.Spec.Platform.GCP = nil

		err := applyGCPWorkloadIdentityFederationWebhookContainer(&corev1.PodSpec{}, hcp)

		g.Expect(err).To(MatchError("gcp platform type requires spec.platform.gcp"))
	})
}

func testGCPHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Region: "us-central1",
				},
			},
		},
	}
	if tlsProfile != nil {
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
			APIServer: &configv1.APIServerSpec{
				TLSSecurityProfile: tlsProfile,
			},
		}
	}
	return hcp
}
