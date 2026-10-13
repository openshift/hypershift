package cvo

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
)

func TestIsManagementClusterMetricsAccessEnabled(t *testing.T) {
	testCases := []struct {
		name                                    string
		enableCVOManagementClusterMetricsAccess bool
		rhobsEnvValue                           string
		hcp                                     *hyperv1.HostedControlPlane
		expected                                bool
	}{
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is true, it should return true regardless of other conditions",
			enableCVOManagementClusterMetricsAccess: true,
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: true,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is true and RHOBS env is set with ROSA HCP, it should return true",
			enableCVOManagementClusterMetricsAccess: true,
			rhobsEnvValue:                           "1",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is false and RHOBS env is not set, it should return false",
			enableCVOManagementClusterMetricsAccess: false,
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: false,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is false and RHOBS env is set but platform is not AWS, it should return false",
			enableCVOManagementClusterMetricsAccess: false,
			rhobsEnvValue:                           "1",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			expected: false,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is false and RHOBS env is set but AWS has no red-hat-managed tag, it should return false",
			enableCVOManagementClusterMetricsAccess: false,
			rhobsEnvValue:                           "1",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "some-other-tag", Value: "some-value"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is false and RHOBS env is 1 and HCP is ROSA HCP, it should return true",
			enableCVOManagementClusterMetricsAccess: false,
			rhobsEnvValue:                           "1",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:                                    "When enableCVOManagementClusterMetricsAccess is false and RHOBS env is 0 and HCP is ROSA HCP, it should return false",
			enableCVOManagementClusterMetricsAccess: false,
			rhobsEnvValue:                           "0",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			t.Setenv(rhobsmonitoring.EnvironmentVariable, tc.rhobsEnvValue)

			cvo := &clusterVersionOperator{
				enableCVOManagementClusterMetricsAccess: tc.enableCVOManagementClusterMetricsAccess,
			}

			cpContext := component.WorkloadContext{
				HCP: tc.hcp,
			}

			result := cvo.isManagementClusterMetricsAccessEnabled(cpContext)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
