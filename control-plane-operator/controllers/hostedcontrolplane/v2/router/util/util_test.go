package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestUseHCPRouter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When platform is IBMCloud, it should return false regardless of other settings",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			expected: false,
		},
		{
			name: "When not ARO HCP and HCP is private with AWS Private endpoint, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  ptr.To(hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}),
					},
				},
			},
			expected: true,
		},
		{
			name: "When not ARO HCP and HCP is private with AWS PublicAndPrivate endpoint, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  ptr.To(hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.PublicAndPrivate}),
					},
				},
			},
			expected: true,
		},
		{
			name: "When not ARO HCP and HCP is public without route labels, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  ptr.To(hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}),
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(UseHCPRouter(tc.hcp)).To(Equal(tc.expected))
		})
	}
}

// TestUseHCPRouterAroHCP tests UseHCPRouter behavior when the MANAGED_SERVICE
// environment variable is set to ARO-HCP. These tests cannot use t.Parallel
// because t.Setenv is incompatible with parallel test execution.
func TestUseHCPRouterAroHCP(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When ARO HCP and HCP is private with swift annotation, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotation: "some-value",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			expected: true,
		},
		{
			name: "When ARO HCP and HCP is not private without swift annotation, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			g := NewWithT(t)
			g.Expect(UseHCPRouter(tc.hcp)).To(Equal(tc.expected))
		})
	}
}
