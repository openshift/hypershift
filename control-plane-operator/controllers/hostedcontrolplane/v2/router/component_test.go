package router

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUseHCPRouter(t *testing.T) {
	tests := []struct {
		name      string
		hcp       *hyperv1.HostedControlPlane
		setupEnv  func(t *testing.T)
		want      bool
	}{
		{
			name: "When platform is IBMCloud it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			want: false,
		},
		{
			name: "When ARO Swift is enabled it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotation: "test-instance",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			want: true,
		},
		{
			name: "When ARO-HCP shared ingress is used it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			want: false,
		},
		{
			name: "When AWS has private endpoint access it should return true",
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
			want: true,
		},
		{
			name: "When AWS has public and private endpoint access it should return true",
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
			want: true,
		},
		{
			name: "When AWS has public endpoint access without DNS it should return false",
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
			want: false,
		},
		{
			name: "When AWS has public endpoint access with DNS for APIServer it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Public,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When GCP has private endpoint access it should return true",
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
			want: true,
		},
		{
			name: "When GCP has public and private endpoint access it should return true",
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
			want: true,
		},
		{
			name: "When platform is None it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv != nil {
				tt.setupEnv(t)
			}
			if got := UseHCPRouter(tt.hcp); got != tt.want {
				t.Errorf("UseHCPRouter() = %v, want %v", got, tt.want)
			}
		})
	}
}
