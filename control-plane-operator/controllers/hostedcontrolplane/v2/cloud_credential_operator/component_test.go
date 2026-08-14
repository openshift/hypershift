package cco

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsAWSPlatform(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		platformType hyperv1.PlatformType
		expected     bool
	}{
		{
			name:         "When platform is AWS, it should return true",
			platformType: hyperv1.AWSPlatform,
			expected:     true,
		},
		{
			name:         "When platform is Azure, it should return false",
			platformType: hyperv1.AzurePlatform,
			expected:     false,
		},
		{
			name:         "When platform is KubeVirt, it should return false",
			platformType: hyperv1.KubevirtPlatform,
			expected:     false,
		},
		{
			name:         "When platform is Agent, it should return false",
			platformType: hyperv1.AgentPlatform,
			expected:     false,
		},
		{
			name:         "When platform is PowerVS, it should return false",
			platformType: hyperv1.PowerVSPlatform,
			expected:     false,
		},
		{
			name:         "When platform is OpenStack, it should return false",
			platformType: hyperv1.OpenStackPlatform,
			expected:     false,
		},
		{
			name:         "When platform is None, it should return false",
			platformType: hyperv1.NonePlatform,
			expected:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := isAWSPlatform(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestCloudCredentialOperatorOptions(t *testing.T) {
	t.Parallel()

	t.Run("When IsRequestServing is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cco := &cloudCredentialOperator{}
		g.Expect(cco.IsRequestServing()).To(BeFalse())
	})

	t.Run("When MultiZoneSpread is called, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cco := &cloudCredentialOperator{}
		g.Expect(cco.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When NeedsManagementKASAccess is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cco := &cloudCredentialOperator{}
		g.Expect(cco.NeedsManagementKASAccess()).To(BeFalse())
	})
}
