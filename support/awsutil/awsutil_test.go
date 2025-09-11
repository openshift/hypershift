package awsutil

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsROSAHCP(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "ROSA HCP with red-hat-managed=true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "true"},
								{Key: "red-hat-clustertype", Value: "rosa"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Non-ROSA HCP with red-hat-managed=false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "false"},
								{Key: "red-hat-clustertype", Value: "rosa"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "HCP without red-hat-managed tag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"},
								{Key: "kubernetes.io/cluster/test", Value: "owned"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "HCP with nil AWS platform",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "HCP with empty resource tags",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "HCP with red-hat-managed but different value",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "yes"},
								{Key: "red-hat-clustertype", Value: "rosa"},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := IsROSAHCP(test.hcp)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestHasResourceTag(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: []hyperv1.AWSResourceTag{
						{Key: "red-hat-managed", Value: "true"},
						{Key: "red-hat-clustertype", Value: "rosa"},
						{Key: "kubernetes.io/cluster/test", Value: "owned"},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		key      string
		value    string
		expected bool
	}{
		{
			name:     "Existing tag with correct value",
			key:      "red-hat-managed",
			value:    "true",
			expected: true,
		},
		{
			name:     "Existing tag with wrong value",
			key:      "red-hat-managed",
			value:    "false",
			expected: false,
		},
		{
			name:     "Non-existing tag",
			key:      "non-existing",
			value:    "value",
			expected: false,
		},
		{
			name:     "Empty key",
			key:      "",
			value:    "value",
			expected: false,
		},
		{
			name:     "Empty value",
			key:      "red-hat-managed",
			value:    "",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := HasResourceTag(hcp, test.key, test.value)
			g.Expect(result).To(Equal(test.expected))
		})
	}

	// Test with nil AWS platform
	t.Run("nil AWS platform", func(t *testing.T) {
		g := NewWithT(t)
		nilHCP := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					AWS: nil,
				},
			},
		}
		result := HasResourceTag(nilHCP, "red-hat-managed", "true")
		g.Expect(result).To(BeFalse())
	})
}

func TestGetResourceTagValue(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: []hyperv1.AWSResourceTag{
						{Key: "red-hat-managed", Value: "true"},
						{Key: "red-hat-clustertype", Value: "rosa"},
						{Key: "kubernetes.io/cluster/test", Value: "owned"},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "Existing tag",
			key:      "red-hat-managed",
			expected: "true",
		},
		{
			name:     "Another existing tag",
			key:      "red-hat-clustertype",
			expected: "rosa",
		},
		{
			name:     "Non-existing tag",
			key:      "non-existing",
			expected: "",
		},
		{
			name:     "Empty key",
			key:      "",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := GetResourceTagValue(hcp, test.key)
			g.Expect(result).To(Equal(test.expected))
		})
	}

	// Test with nil AWS platform
	t.Run("nil AWS platform", func(t *testing.T) {
		g := NewWithT(t)
		nilHCP := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					AWS: nil,
				},
			},
		}
		result := GetResourceTagValue(nilHCP, "red-hat-managed")
		g.Expect(result).To(Equal(""))
	})
}

func TestHasResourceTagKey(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: []hyperv1.AWSResourceTag{
						{Key: "red-hat-managed", Value: "true"},
						{Key: "red-hat-clustertype", Value: "rosa"},
						{Key: "kubernetes.io/cluster/test", Value: "owned"},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		key      string
		expected bool
	}{
		{
			name:     "Existing key",
			key:      "red-hat-managed",
			expected: true,
		},
		{
			name:     "Another existing key",
			key:      "red-hat-clustertype",
			expected: true,
		},
		{
			name:     "Non-existing key",
			key:      "non-existing",
			expected: false,
		},
		{
			name:     "Empty key",
			key:      "",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := HasResourceTagKey(hcp, test.key)
			g.Expect(result).To(Equal(test.expected))
		})
	}

	// Test with nil AWS platform
	t.Run("nil AWS platform", func(t *testing.T) {
		g := NewWithT(t)
		nilHCP := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					AWS: nil,
				},
			},
		}
		result := HasResourceTagKey(nilHCP, "red-hat-managed")
		g.Expect(result).To(BeFalse())
	})
}

func TestHasResourceTagWithEmptyTags(t *testing.T) {
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: []hyperv1.AWSResourceTag{},
				},
			},
		},
	}

	// Test all functions with empty tags
	g.Expect(IsROSAHCP(hcp)).To(BeFalse())
	g.Expect(HasResourceTag(hcp, "any-key", "any-value")).To(BeFalse())
	g.Expect(GetResourceTagValue(hcp, "any-key")).To(Equal(""))
	g.Expect(HasResourceTagKey(hcp, "any-key")).To(BeFalse())
}
