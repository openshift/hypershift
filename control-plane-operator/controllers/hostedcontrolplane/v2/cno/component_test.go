package cno

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestPlatformHasCloudNetworkConfigController(t *testing.T) {
	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		expected     bool
	}{
		{
			name:         "When platform is AWS it should have cloud-network-config-controller",
			platformType: hyperv1.AWSPlatform,
			expected:     true,
		},
		{
			name:         "When platform is Azure it should have cloud-network-config-controller",
			platformType: hyperv1.AzurePlatform,
			expected:     true,
		},
		{
			name:         "When platform is GCP it should have cloud-network-config-controller",
			platformType: hyperv1.GCPPlatform,
			expected:     true,
		},
		{
			name:         "When platform is OpenStack it should have cloud-network-config-controller",
			platformType: hyperv1.OpenStackPlatform,
			expected:     true,
		},
		{
			name:         "When platform is KubeVirt it should not have cloud-network-config-controller",
			platformType: hyperv1.KubevirtPlatform,
			expected:     false,
		},
		{
			name:         "When platform is Agent it should not have cloud-network-config-controller",
			platformType: hyperv1.AgentPlatform,
			expected:     false,
		},
		{
			name:         "When platform is None it should not have cloud-network-config-controller",
			platformType: hyperv1.NonePlatform,
			expected:     false,
		},
		{
			name:         "When platform is IBMCloud it should not have cloud-network-config-controller",
			platformType: hyperv1.IBMCloudPlatform,
			expected:     false,
		},
		{
			name:         "When platform is PowerVS it should not have cloud-network-config-controller",
			platformType: hyperv1.PowerVSPlatform,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := platformHasCloudNetworkConfigController(tt.platformType)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
