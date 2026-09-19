package kas

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/blang/semver"
)

func TestResolveKASVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) hyperv1.KubeAPIServerOperatorSpec {
		return hyperv1.KubeAPIServerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: l},
		}
	}

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected int
	}{
		{
			name: "When no operatorConfiguration is set, it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 2,
		},
		{
			name: "When operatorConfiguration exists but kubeAPIServer is nil, it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expected: 2,
		},
		{
			name: "When kubeAPIServer logLevel is Normal, it should return verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: logLevel(hyperv1.Normal),
					},
				},
			},
			expected: 2,
		},
		{
			name: "When kubeAPIServer logLevel is Debug, it should return verbosity 4",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: logLevel(hyperv1.Debug),
					},
				},
			},
			expected: 4,
		},
		{
			name: "When kubeAPIServer logLevel is Trace, it should return verbosity 6",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: logLevel(hyperv1.Trace),
					},
				},
			},
			expected: 6,
		},
		{
			name: "When kubeAPIServer logLevel is TraceAll, it should return verbosity 8",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: logLevel(hyperv1.TraceAll),
					},
				},
			},
			expected: 8,
		},
		{
			name: "When only annotation is set, it should honor the annotation",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.KubeAPIServerVerbosityLevelAnnotation: "5",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 5,
		},
		{
			name: "When both annotation and API field are set, it should prefer API field",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.KubeAPIServerVerbosityLevelAnnotation: "5",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: logLevel(hyperv1.TraceAll),
					},
				},
			},
			expected: 8,
		},
		{
			name: "When annotation has invalid value, it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.KubeAPIServerVerbosityLevelAnnotation: "not-a-number",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resolveKASVerbosity(tt.hcp)).To(Equal(tt.expected))
		})
	}
}

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

func TestAdaptDeploymentKASLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected string
	}{
		{
			name:     "When no operatorConfiguration is set, it should default to --v=2",
			hcp:      &hyperv1.HostedControlPlane{},
			expected: "--v=2",
		},
		{
			name: "When logLevel is Debug, it should set --v=4",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeAPIServer: hyperv1.KubeAPIServerOperatorSpec{
							ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Debug},
						},
					},
				},
			},
			expected: "--v=4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  ComponentName,
									Ports: []corev1.ContainerPort{{ContainerPort: 6443}},
								},
							},
						},
					},
				},
			}
			cpContext := component.WorkloadContext{
				HCP:                      tt.hcp,
				ReleaseImageProvider:     testutil.FakeImageProvider(),
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}
			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())
			container := findContainerByNameInPod(&deployment.Spec.Template.Spec, ComponentName)
			g.Expect(container).NotTo(BeNil())
			g.Expect(container.Args).To(ContainElement(tt.expected))
		})
	}
}

func TestAddImagePrePullInitContainers(t *testing.T) {
	testCases := []struct {
		name                  string
		containers            []corev1.Container
		expectedPrePullImages []string
	}{
		{
			name: "When kube-apiserver container exists, it should pre-pull only the apiserver image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "bootstrap", Image: "registry.io/controlplane-operator:v1"},
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
			},
			expectedPrePullImages: []string{"registry.io/kube-apiserver:v1"},
		},
		{
			name: "When kube-apiserver container does not exist, it should not create pre-pull init containers",
			containers: []corev1.Container{
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
				{Name: "audit-logs", Image: "registry.io/cli:v1"},
			},
			expectedPrePullImages: []string{},
		},
		{
			name:                  "When there are no containers, it should have no pre-pull init containers",
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
			name: "When TLS security profile is nil, it should use default Intermediate profile",
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
			name: "When TLS security profile is Old, it should add TLS configuration",
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
			name: "When TLS security profile is Intermediate, it should add TLS configuration",
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
			name: "When TLS security profile is Modern, it should add TLS 1.3 configuration",
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
			name: "When TLS security profile is Custom with TLS 1.2, it should add tls-min-version flag",
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
			name: "When TLS security profile is Custom with TLS 1.3, it should add tls-min-version flag",
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
			err := applyAWSPodIdentityWebhookContainer(podSpec, tc.hcp, semver.MustParse("4.22.0"))
			g.Expect(err).ToNot(HaveOccurred())
			tc.validatePod(g, podSpec)
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	for _, tc := range []struct {
		version         string
		userVersion     string
		konnectivityTLS bool
		webhookTLS      bool
		invalidVersion  bool
	}{
		{version: "4.20.0", userVersion: "4.23.0"},
		{version: "4.21.32-candidate", userVersion: "4.23.0"},
		{version: "4.21.32", userVersion: "4.23.0"},
		{version: "4.22.0-rc.0", userVersion: "4.21.32", webhookTLS: true},
		{version: "4.22.0", userVersion: "4.21.32", webhookTLS: true},
		{version: "4.23.0-0.nightly-2026-09-08-022115", userVersion: "4.21.32", konnectivityTLS: true, webhookTLS: true},
		{version: "4.23.0", userVersion: "4.21.32", konnectivityTLS: true, webhookTLS: true},
		{version: "5.0.0", userVersion: "4.21.32", konnectivityTLS: true, webhookTLS: true},
		{version: "", invalidVersion: true},
		{version: "invalid", invalidVersion: true},
	} {
		t.Run(fmt.Sprintf("When the control plane release is %q, it should use only supported sidecar TLS flags", tc.version), func(t *testing.T) {
			for _, profile := range []struct {
				name  string
				value *configv1.TLSSecurityProfile
				min   string
			}{
				{name: "default", min: "VersionTLS12"},
				{name: "Modern", value: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType}, min: "VersionTLS13"},
				{name: "invalid", value: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType}},
			} {
				t.Run(fmt.Sprintf("When the TLS profile is %s, it should preserve supported configuration or return an error", profile.name), func(t *testing.T) {
					g := NewWithT(t)
					hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{}},
						Configuration: &hyperv1.ClusterConfiguration{APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: profile.value,
						}},
					}}
					deployment := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "konnectivity-server", Args: []string{"--cluster-cert=/etc/konnectivity/server/tls.crt"}}},
					}}}}
					err := adaptDeployment(component.WorkloadContext{
						HCP:                      hcp,
						ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion(tc.version)),
						UserReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion(tc.userVersion)),
					}, deployment)
					if tc.invalidVersion || profile.name == "invalid" {
						g.Expect(err).To(HaveOccurred())
						return
					}
					g.Expect(err).NotTo(HaveOccurred())
					server := findContainerByNameInPod(&deployment.Spec.Template.Spec, "konnectivity-server")
					g.Expect(server.Args).To(ContainElements("--server-count", "--cluster-cert=/etc/konnectivity/server/tls.crt"))
					g.Expect(slices.Contains(server.Args, "--tls-min-version="+profile.min)).To(Equal(tc.konnectivityTLS))
					g.Expect(slices.ContainsFunc(server.Args, func(arg string) bool {
						return strings.HasPrefix(arg, "--cipher-suites=")
					})).To(Equal(profile.name != "Modern"), "older konnectivity still supports cipher suites")
					webhook := findContainerByNameInPod(&deployment.Spec.Template.Spec, "azure-workload-identity-webhook")
					g.Expect(webhook).NotTo(BeNil())
					g.Expect(webhook.Args).To(HaveLen(1))
					g.Expect(strings.Contains(webhook.Args[0], "--tls-min-version="+profile.min)).To(Equal(tc.webhookTLS))
					g.Expect(strings.Contains(webhook.Args[0], "--tls-cipher-suites=")).To(Equal(tc.webhookTLS && profile.name != "Modern"))
					g.Expect(webhook.Args[0]).To(ContainSubstring("--webhook-cert-dir=/var/run/app/certs"))

					hcp.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}}
					awsDeployment := &appsv1.Deployment{}
					err = adaptDeployment(component.WorkloadContext{
						HCP:                      hcp,
						ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion(tc.version)),
						UserReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion(tc.userVersion)),
					}, awsDeployment)
					g.Expect(err).NotTo(HaveOccurred())
					awsWebhook := findContainerByNameInPod(&awsDeployment.Spec.Template.Spec, "aws-pod-identity-webhook")
					g.Expect(awsWebhook).NotTo(BeNil())
					g.Expect(slices.Contains(awsWebhook.Command, "--tls-min-version="+profile.min)).To(Equal(tc.webhookTLS))
					g.Expect(slices.ContainsFunc(awsWebhook.Command, func(arg string) bool {
						return strings.HasPrefix(arg, "--tls-cipher-suites=")
					})).To(Equal(tc.webhookTLS && profile.name != "Modern"))
					g.Expect(awsWebhook.Command).To(ContainElement("--tls-cert=/var/run/app/certs/tls.crt"))
				})
			}
		})
	}
}

func TestKonnectivityServerTLSMinVersion(t *testing.T) {
	testCases := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		expectedMinTLS string
	}{
		{
			name:           "When TLS security profile is nil, it should use default Intermediate profile for konnectivity-server",
			expectedMinTLS: "VersionTLS12",
			hcp:            &hyperv1.HostedControlPlane{},
		},
		{
			name:           "When TLS security profile is Old, it should set TLS 1.0 for konnectivity-server",
			expectedMinTLS: "VersionTLS10",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileOldType,
							},
						},
					},
				},
			},
		},
		{
			name:           "When TLS security profile is Intermediate, it should set TLS 1.2 for konnectivity-server",
			expectedMinTLS: "VersionTLS12",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileIntermediateType,
							},
						},
					},
				},
			},
		},
		{
			name:           "When TLS security profile is Modern, it should set TLS 1.3 for konnectivity-server",
			expectedMinTLS: "VersionTLS13",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileModernType,
							},
						},
					},
				},
			},
		},
		{
			name:           "When TLS security profile is Custom with TLS 1.2, it should set TLS 1.2 for konnectivity-server",
			expectedMinTLS: "VersionTLS12",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										MinTLSVersion: configv1.VersionTLS12,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:           "When TLS security profile is Custom with TLS 1.3, it should set TLS 1.3 for konnectivity-server",
			expectedMinTLS: "VersionTLS13",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP:                      tc.hcp,
				ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion("4.23.0")),
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "konnectivity-server",
									Image: "konnectivity-server:latest",
									Args:  []string{},
								},
							},
						},
					},
				},
			}

			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := findContainerByNameInPod(&deployment.Spec.Template.Spec, "konnectivity-server")
			g.Expect(container).NotTo(BeNil())
			expected := fmt.Sprintf("--tls-min-version=%s", tc.expectedMinTLS)
			g.Expect(container.Args).To(ContainElement(expected))
		})
	}
}
