package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsPrivateHCP(t *testing.T) {
	type args struct {
		hcp *hyperv1.HostedControlPlane
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "AWS Public",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Public,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "AWS PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.PublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "AWS Private",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Private,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "GCP Private",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.GCPPlatform,
							GCP: &hyperv1.GCPPlatformSpec{
								EndpointAccess: hyperv1.GCPEndpointAccessPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "GCP PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.GCPPlatform,
							GCP: &hyperv1.GCPPlatformSpec{
								EndpointAccess: hyperv1.GCPEndpointAccessPublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "None",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.NonePlatform,
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPrivateHCP(tt.args.hcp); got != tt.want {
				t.Errorf("IsPrivateHCP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPublicHCP(t *testing.T) {
	type args struct {
		hcp *hyperv1.HostedControlPlane
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "AWS Public",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Public,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "AWS PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.PublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "AWS Private",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Private,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "GCP Private",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.GCPPlatform,
							GCP: &hyperv1.GCPPlatformSpec{
								EndpointAccess: hyperv1.GCPEndpointAccessPrivate,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "GCP PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.GCPPlatform,
							GCP: &hyperv1.GCPPlatformSpec{
								EndpointAccess: hyperv1.GCPEndpointAccessPublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "None",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.NonePlatform,
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPublicHCP(tt.args.hcp); got != tt.want {
				t.Errorf("IsPublicHCP() = %v, want %v", got, tt.want)
			}
		})
	}
}
