package autoscaler

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestGetIgnoreLabels(t *testing.T) {
	tests := []struct {
		name                           string
		platformType                   hyperv1.PlatformType
		expectedCommonLabels           []string
		expectedPlatformSpecificLabels []string
	}{
		{
			name:         "AWS platform",
			platformType: hyperv1.AWSPlatform,
			expectedCommonLabels: []string{
				CommonIgnoredLabelNodePool,
				CommonIgnoredLabelAWSEBSZone,
				CommonIgnoredLabelAzureDiskZone,
				CommonIgnoredLabelIBMCloudWorkerID,
				CommonIgnoredLabelVPCBlockCSIDriver,
			},
			expectedPlatformSpecificLabels: []string{
				AwsIgnoredLabelK8sEniconfig,
				AwsIgnoredLabelLifecycle,
				AwsIgnoredLabelZoneID,
			},
		},
		{
			name:         "Azure platform",
			platformType: hyperv1.AzurePlatform,
			expectedCommonLabels: []string{
				CommonIgnoredLabelNodePool,
				CommonIgnoredLabelAWSEBSZone,
				CommonIgnoredLabelAzureDiskZone,
				CommonIgnoredLabelIBMCloudWorkerID,
				CommonIgnoredLabelVPCBlockCSIDriver,
			},
			expectedPlatformSpecificLabels: []string{
				AzureNodepoolLegacyLabel,
				AzureNodepoolLabel,
			},
		},
		{
			name:         "Other platform",
			platformType: hyperv1.KubevirtPlatform,
			expectedCommonLabels: []string{
				CommonIgnoredLabelNodePool,
				CommonIgnoredLabelAWSEBSZone,
				CommonIgnoredLabelAzureDiskZone,
				CommonIgnoredLabelIBMCloudWorkerID,
				CommonIgnoredLabelVPCBlockCSIDriver,
			},
			expectedPlatformSpecificLabels: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := GetIgnoreLabels(tt.platformType)

			// Check that all common labels are present
			for _, expectedLabel := range tt.expectedCommonLabels {
				found := false
				for _, label := range labels {
					if label == expectedLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected common label %q not found in result", expectedLabel)
				}
			}

			// Check that all platform-specific labels are present
			for _, expectedLabel := range tt.expectedPlatformSpecificLabels {
				found := false
				for _, label := range labels {
					if label == expectedLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected platform-specific label %q not found in result", expectedLabel)
				}
			}

			// Verify total count
			expectedTotal := len(tt.expectedCommonLabels) + len(tt.expectedPlatformSpecificLabels)
			if len(labels) != expectedTotal {
				t.Errorf("Expected %d labels, got %d", expectedTotal, len(labels))
			}
		})
	}
}
