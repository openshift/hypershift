package configoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

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
