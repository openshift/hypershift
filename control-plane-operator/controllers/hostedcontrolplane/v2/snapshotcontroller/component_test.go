package snapshotcontroller

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

func TestIsStorageAndCSIManaged(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		platform hyperv1.PlatformType
		expected bool
	}{
		{
			name:     "When platform is IBMCloud, it should return false",
			platform: hyperv1.IBMCloudPlatform,
			expected: false,
		},
		{
			name:     "When platform is PowerVS, it should return false",
			platform: hyperv1.PowerVSPlatform,
			expected: false,
		},
		{
			name:     "When platform is AWS, it should return true",
			platform: hyperv1.AWSPlatform,
			expected: true,
		},
		{
			name:     "When platform is Azure, it should return true",
			platform: hyperv1.AzurePlatform,
			expected: true,
		},
		{
			name:     "When platform is KubeVirt, it should return true",
			platform: hyperv1.KubevirtPlatform,
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: tc.platform,
						},
					},
				},
			}

			result, err := isStorageAndCSIManaged(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
