package configoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

type fakeReleaseImageProvider struct{}

func (fakeReleaseImageProvider) GetImage(string) string {
	return "quay.io/example/image:latest"
}

func (fakeReleaseImageProvider) ImageExist(string) (string, bool) {
	return "quay.io/example/image:latest", true
}

func (fakeReleaseImageProvider) Version() string {
	return "test-version"
}

func (fakeReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{"kubernetes": "test-kubernetes-version"}, nil
}

func (fakeReleaseImageProvider) ComponentImages() map[string]string {
	return map[string]string{}
}

func TestAdaptDeploymentIBMCloudControllerSelection(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	deployment := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: ComponentName}},
				},
			},
		},
	}
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
		},
	}

	err := (&hcco{}).adaptDeployment(controlplanecomponent.WorkloadContext{
		HCP:                  hcp,
		ReleaseImageProvider: fakeReleaseImageProvider{},
	}, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(deployment.Spec.Template.Spec.Containers[0].Command).To(ContainElement(
		"--controllers=controller-manager-ca,resources,user-ca-bundle,inplaceupgrader,drainer,hcpstatus",
	))
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
