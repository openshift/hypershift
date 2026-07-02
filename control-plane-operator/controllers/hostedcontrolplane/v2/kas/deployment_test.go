package kas

import (
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
)

// findContainerByNameInPod finds a container by name in a PodSpec and returns a pointer to it.
// Returns nil if the container is not found.
func findContainerByNameInPod(podSpec *corev1.PodSpec, name string) *corev1.Container {
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == name {
			return &podSpec.Containers[i]
		}
	}
	return nil
}

func TestAddImagePrePullInitContainers(t *testing.T) {
	testCases := []struct {
		name                  string
		containers            []corev1.Container
		expectedPrePullImages []string
	}{
		{
			name: "When kube-apiserver container exists it should pre-pull only the apiserver image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "bootstrap", Image: "registry.io/controlplane-operator:v1"},
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
			},
			expectedPrePullImages: []string{"registry.io/kube-apiserver:v1"},
		},
		{
			name: "When kube-apiserver container does not exist it should not create pre-pull init containers",
			containers: []corev1.Container{
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
				{Name: "audit-logs", Image: "registry.io/cli:v1"},
			},
			expectedPrePullImages: []string{},
		},
		{
			name:                  "When there are no containers it should have no pre-pull init containers",
			containers:            []corev1.Container{},
			expectedPrePullImages: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			podSpec := &corev1.PodSpec{
				Containers: tc.containers,
			}

			addImagePrePullInitContainers(podSpec)

			// Find pre-pull init containers and their positions
			var prePullInitContainers []corev1.Container
			firstOtherInitContainerIndex := -1
			lastPrePullInitContainerIndex := -1

			for i, initContainer := range podSpec.InitContainers {
				if strings.HasPrefix(initContainer.Name, "pre-pull-image-") {
					prePullInitContainers = append(prePullInitContainers, initContainer)
					lastPrePullInitContainerIndex = i
				} else {
					if firstOtherInitContainerIndex == -1 {
						firstOtherInitContainerIndex = i
					}
				}
			}

			// Validate the expected number of pre-pull init containers
			g.Expect(len(prePullInitContainers)).To(Equal(len(tc.expectedPrePullImages)),
				"unexpected number of pre-pull init containers")

			// Validate that pre-pull init containers use the expected images
			prePullImages := make([]string, 0, len(prePullInitContainers))
			for _, c := range prePullInitContainers {
				prePullImages = append(prePullImages, c.Image)
			}
			g.Expect(prePullImages).To(Equal(tc.expectedPrePullImages),
				"pre-pull init containers have unexpected images")

			// Validate that pre-pull init containers come before other init containers
			if firstOtherInitContainerIndex != -1 && lastPrePullInitContainerIndex != -1 {
				g.Expect(lastPrePullInitContainerIndex).To(BeNumerically("<", firstOtherInitContainerIndex),
					"pre-pull init containers must come before other init containers")
			}
		})
	}
}

func TestApplyAWSPodIdentityWebhookContainer(t *testing.T) {
	testCases := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		validatePod func(*GomegaWithT, *corev1.PodSpec)
	}{
		{
			name: "When TLS security profile is nil it should use default Intermediate profile",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS12"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeTrue())
			},
		},
		{
			name: "When TLS security profile is Old it should add TLS configuration",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileOldType,
							},
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS10"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeTrue())
			},
		},
		{
			name: "When TLS security profile is Intermediate it should add TLS configuration",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileIntermediateType,
							},
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS12"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeTrue())
			},
		},
		{
			name: "When TLS security profile is Modern it should add TLS 1.3 configuration",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileModernType,
							},
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS13"))
				g.Expect(slices.ContainsFunc(webhookContainer.Command, func(arg string) bool {
					return strings.HasPrefix(arg, "--tls-cipher-suites=")
				})).To(BeFalse())
			},
		},
		{
			name: "When TLS security profile is Custom with TLS 1.2 it should add tls-min-version flag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										MinTLSVersion: configv1.VersionTLS12,
										Ciphers: []string{
											"ECDHE-ECDSA-AES128-GCM-SHA256",
											"ECDHE-RSA-AES128-GCM-SHA256",
										},
									},
								},
							},
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS12"))
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			},
		},
		{
			name: "When TLS security profile is Custom with TLS 1.3 it should add tls-min-version flag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										MinTLSVersion: configv1.VersionTLS13,
									},
								},
							},
						},
					},
				},
			},
			validatePod: func(g *GomegaWithT, podSpec *corev1.PodSpec) {
				webhookContainer := findContainerByNameInPod(podSpec, "aws-pod-identity-webhook")
				g.Expect(webhookContainer).NotTo(BeNil())
				g.Expect(webhookContainer.Command).To(ContainElement("--tls-min-version=VersionTLS13"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			podSpec := &corev1.PodSpec{}
			applyAWSPodIdentityWebhookContainer(podSpec, tc.hcp)
			tc.validatePod(g, podSpec)
		})
	}
}
