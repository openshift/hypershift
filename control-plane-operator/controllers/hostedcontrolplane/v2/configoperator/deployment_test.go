package configoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/imageresolution"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	gomock "go.uber.org/mock/gomock"
)

func TestAdaptDeployment(t *testing.T) {
	tests := []struct {
		name                       string
		registryOverrides          map[string]string
		imageRegistryMirrors       map[string][]string
		expectedRegistryOverrides  string
		expectedImgOverridesEnvVar string
	}{
		{
			name: "When resolver config has registry overrides, it should serialize them into --registry-overrides flag",
			registryOverrides: map[string]string{
				"quay.io/openshift-release-dev": "registry.example.com/ocp",
				"registry.redhat.io":            "registry.example.com/redhat",
			},
			expectedRegistryOverrides:  "quay.io/openshift-release-dev=registry.example.com/ocp,registry.redhat.io=registry.example.com/redhat",
			expectedImgOverridesEnvVar: "",
		},
		{
			name: "When resolver config has image registry mirrors, it should serialize them into OPENSHIFT_IMG_OVERRIDES env var",
			imageRegistryMirrors: map[string][]string{
				"quay.io":            {"mirror-registry.example.com", "backup-mirror.example.com"},
				"registry.redhat.io": {"mirror-registry.example.com"},
			},
			expectedRegistryOverrides:  "",
			expectedImgOverridesEnvVar: "quay.io=mirror-registry.example.com,quay.io=backup-mirror.example.com,registry.redhat.io=mirror-registry.example.com",
		},
		{
			name:                       "When resolver config is empty, it should produce empty override strings",
			registryOverrides:          nil,
			imageRegistryMirrors:       nil,
			expectedRegistryOverrides:  "",
			expectedImgOverridesEnvVar: "",
		},
		{
			name: "When resolver config has both overrides and mirrors, it should serialize both correctly",
			registryOverrides: map[string]string{
				"quay.io/openshift-release-dev": "registry.example.com/ocp",
			},
			imageRegistryMirrors: map[string][]string{
				"registry.redhat.io": {"mirror-registry.example.com"},
			},
			expectedRegistryOverrides:  "quay.io/openshift-release-dev=registry.example.com/ocp",
			expectedImgOverridesEnvVar: "registry.redhat.io=mirror-registry.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)

			mockProvider := imageprovider.NewMockReleaseImageProvider(ctrl)
			mockProvider.EXPECT().ComponentVersions().Return(map[string]string{
				"kubernetes": "1.31.0",
			}, nil)
			mockProvider.EXPECT().GetImage("hosted-cluster-config-operator").Return("quay.io/openshift-release-dev/hcco:latest")
			mockProvider.EXPECT().Version().Return("4.18.0")

			client := crfake.NewClientBuilder().WithScheme(api.Scheme).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.18-x86_64",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			}

			cpContext := component.WorkloadContext{
				Context:              t.Context(),
				Client:               client,
				HCP:                  hcp,
				ReleaseImageProvider: mockProvider,
				InfraStatus: infra.InfrastructureStatus{
					KonnectivityHost: "konnectivity.example.com",
					KonnectivityPort: 8091,
					OAuthHost:        "oauth.example.com",
					OAuthPort:        6443,
				},
			}

			resolverConfig := imageresolution.ResolverConfig{
				RegistryOverrides:    tt.registryOverrides,
				ImageRegistryMirrors: tt.imageRegistryMirrors,
			}
			h := &hcco{
				resolverConfig: resolverConfig,
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentName,
					Namespace: hcp.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: ComponentName},
							},
							Volumes: []corev1.Volume{
								{Name: kubeconfigVolumeName, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{}}},
								{Name: rootCAVolumeName, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
								{Name: clusterSignerCAVolumeName, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
							},
						},
					},
				},
			}

			err := h.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			var container *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == ComponentName {
					container = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			g.Expect(container).ToNot(BeNil(), "container %q not found", ComponentName)

			// Verify --registry-overrides flag
			var registryOverridesValue string
			for i, arg := range container.Command {
				if arg == "--registry-overrides" && i+1 < len(container.Command) {
					registryOverridesValue = container.Command[i+1]
					break
				}
			}
			g.Expect(registryOverridesValue).To(Equal(tt.expectedRegistryOverrides),
				"--registry-overrides flag value should match expected serialization")

			// Verify OPENSHIFT_IMG_OVERRIDES env var
			var imgOverridesValue string
			for _, env := range container.Env {
				if env.Name == "OPENSHIFT_IMG_OVERRIDES" {
					imgOverridesValue = env.Value
					break
				}
			}
			g.Expect(imgOverridesValue).To(Equal(tt.expectedImgOverridesEnvVar),
				"OPENSHIFT_IMG_OVERRIDES env var should match expected serialization")
		})
	}
}

func TestIsExternalInfraKubevirt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When HCP has no kubevirt platform, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt platform has no credentials, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: nil,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have no InfraKubeConfigSecret, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: nil,
								InfraNamespace:        "infra-ns",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have InfraKubeConfigSecret but empty InfraNamespace, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "infra-kubeconfig",
									Key:  "kubeconfig",
								},
								InfraNamespace: "",
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When kubevirt credentials have both InfraKubeConfigSecret and InfraNamespace, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							Credentials: &hyperv1.KubevirtPlatformCredentials{
								InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
									Name: "infra-kubeconfig",
									Key:  "kubeconfig",
								},
								InfraNamespace: "infra-ns",
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := isExternalInfraKubevirt(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
