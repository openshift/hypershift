package awsutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsROSAHCP(t *testing.T) {
	tests := []struct {
		name string
		hcp  *hyperv1.HostedControlPlane
		want bool
	}{
		{
			name: "When HCP has red-hat-managed=true tag, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{
									Key:   "red-hat-managed",
									Value: "true",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When HCP has red-hat-managed=true tag among other tags, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{
									Key:   "environment",
									Value: "production",
								},
								{
									Key:   "red-hat-managed",
									Value: "true",
								},
								{
									Key:   "team",
									Value: "platform",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When HCP has red-hat-managed=false tag, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{
									Key:   "red-hat-managed",
									Value: "false",
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When HCP has no red-hat-managed tag, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{
									Key:   "environment",
									Value: "dev",
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When HCP has no resource tags, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When HCP is not on AWS platform, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type:  hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{},
					},
				},
			},
			want: false,
		},
		{
			name: "When HCP has nil AWS spec, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  nil,
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsROSAHCP(tt.hcp); got != tt.want {
				t.Errorf("IsROSAHCP() = %v, want %v", got, tt.want)
			}
		})
	}
}
