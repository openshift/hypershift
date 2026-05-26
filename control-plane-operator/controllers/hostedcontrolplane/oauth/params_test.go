package oauth

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockReleaseImageProvider is a simple mock for testing
type mockReleaseImageProvider struct{}

func (m *mockReleaseImageProvider) GetImage(name string) string {
	return "test-image:" + name
}

func (m *mockReleaseImageProvider) ImageExist(key string) (string, bool) {
	return "test-image:" + key, true
}

func (m *mockReleaseImageProvider) Version() string {
	return "4.14.0"
}

func (m *mockReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{}, nil
}

func (m *mockReleaseImageProvider) ComponentImages() map[string]string {
	return map[string]string{}
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxyMergeSemantics(t *testing.T) {
	tests := []struct {
		name                 string
		platformType         hyperv1.PlatformType
		ibmCloudSpec         *hyperv1.IBMCloudPlatformSpec
		expectedOAuthNoProxy []string
		description          string
	}{
		{
			name:         "When Platform.Type is IBMCloud and IBMCloud is nil, OAuthNoProxy contains only defaults",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: nil,
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
			description: "IBMCloud platform with nil IBMCloud spec should add default IBM Cloud endpoints",
		},
		{
			name:         "When IBMCloud is non-nil but OAuthNoProxyEndpoints is empty, OAuthNoProxy contains only defaults",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{},
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
			},
			description: "IBMCloud platform with empty OAuthNoProxyEndpoints should add only default IBM Cloud endpoints",
		},
		{
			name:         "When IBMCloud.OAuthNoProxyEndpoints contains values, OAuthNoProxy contains defaults plus those endpoints",
			platformType: hyperv1.IBMCloudPlatform,
			ibmCloudSpec: &hyperv1.IBMCloudPlatformSpec{
				OAuthNoProxyEndpoints: []string{
					"custom.endpoint1.ibm.com",
					"custom.endpoint2.ibm.com",
				},
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint1.ibm.com",
				"custom.endpoint2.ibm.com",
			},
			description: "IBMCloud platform with custom OAuthNoProxyEndpoints should merge defaults with custom endpoints",
		},
		{
			name:         "When Platform.Type is not IBMCloud, OAuthNoProxy contains only base defaults",
			platformType: hyperv1.AWSPlatform,
			ibmCloudSpec: nil,
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
			},
			description: "Non-IBMCloud platform should not add IBM Cloud specific endpoints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct HostedControlPlane with the specified platform configuration
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type:     tt.platformType,
						IBMCloud: tt.ibmCloudSpec,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.test.com",
						Port: 6443,
					},
				},
			}

			// Create mock release image provider
			releaseImageProvider := &mockReleaseImageProvider{}

			// Call NewOAuthServerParams
			params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

			// Assert OAuthNoProxy equals the expected slice
			g := NewWithT(t)
			g.Expect(params.OAuthNoProxy).To(Equal(tt.expectedOAuthNoProxy), tt.description)
		})
	}
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxyOrder(t *testing.T) {
	// Test that verifies the order of entries in OAuthNoProxy
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					OAuthNoProxyEndpoints: []string{
						"endpoint1.ibm.com",
						"endpoint2.ibm.com",
						"endpoint3.ibm.com",
					},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: "api.test.com",
				Port: 6443,
			},
		},
	}

	releaseImageProvider := &mockReleaseImageProvider{}
	params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

	// Expected order: base defaults, IBM defaults, then custom endpoints
	expectedOrder := []string{
		manifests.KubeAPIServerService("").Name,
		config.AuditWebhookService,
		"iam.cloud.ibm.com",
		"iam.test.cloud.ibm.com",
		"endpoint1.ibm.com",
		"endpoint2.ibm.com",
		"endpoint3.ibm.com",
	}

	g := NewWithT(t)
	g.Expect(params.OAuthNoProxy).To(Equal(expectedOrder))
}

func TestNewOAuthServerParams_NonIBMCloudPlatforms(t *testing.T) {
	// Test various non-IBMCloud platforms to ensure they don't get IBM Cloud endpoints
	platforms := []hyperv1.PlatformType{
		hyperv1.AWSPlatform,
		hyperv1.AzurePlatform,
		hyperv1.KubevirtPlatform,
		hyperv1.AgentPlatform,
		hyperv1.PowerVSPlatform,
		hyperv1.NonePlatform,
	}

	for _, platform := range platforms {
		t.Run(string(platform), func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: platform,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.test.com",
						Port: 6443,
					},
				},
			}

			releaseImageProvider := &mockReleaseImageProvider{}
			params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

			// Should only have base defaults, no IBM Cloud endpoints
			expectedOAuthNoProxy := []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
			}

			g := NewWithT(t)
			g.Expect(params.OAuthNoProxy).To(Equal(expectedOAuthNoProxy))

			// Explicitly verify IBM Cloud endpoints are not present
			g.Expect(params.OAuthNoProxy).ToNot(ContainElement("iam.cloud.ibm.com"))
			g.Expect(params.OAuthNoProxy).ToNot(ContainElement("iam.test.cloud.ibm.com"))
		})
	}
}

func TestNewOAuthServerParams_BasicFields(t *testing.T) {
	// Test that other basic fields are set correctly
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
			},
			ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			AuditWebhook: &corev1.LocalObjectReference{
				Name: "audit-webhook-secret",
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: "api.test.com",
				Port: 6443,
			},
		},
	}

	releaseImageProvider := &mockReleaseImageProvider{}
	params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

	// Verify basic fields
	g := NewWithT(t)
	g.Expect(params.ExternalHost).To(Equal("oauth.test.com"))
	g.Expect(params.ExternalPort).To(Equal(int32(443)))
	g.Expect(params.ExternalAPIHost).To(Equal("api.test.com"))
	g.Expect(params.ExternalAPIPort).To(Equal(int32(6443)))
	g.Expect(params.Availability).To(Equal(hyperv1.HighlyAvailable))
	g.Expect(params.AuditWebhookRef).ToNot(BeNil())
	g.Expect(params.AuditWebhookRef.Name).To(Equal("audit-webhook-secret"))
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxyDeduplication(t *testing.T) {
	tests := []struct {
		name                 string
		customEndpoints      []string
		expectedOAuthNoProxy []string
		description          string
	}{
		{
			name: "When custom endpoints contain duplicates of defaults, duplicates should be filtered out",
			customEndpoints: []string{
				"iam.cloud.ibm.com", // duplicate of default
				"custom.endpoint.ibm.com",
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.ibm.com",
			},
			description: "Duplicates of default endpoints in custom list should be filtered out",
		},
		{
			name: "When custom endpoints contain internal duplicates, only first occurrence should be kept",
			customEndpoints: []string{
				"custom.endpoint.ibm.com",
				"custom.endpoint.ibm.com", // duplicate
				"another.endpoint.ibm.com",
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.ibm.com",
				"another.endpoint.ibm.com",
			},
			description: "Internal duplicates in custom endpoints should be filtered out",
		},
		{
			name: "When custom endpoints duplicate base defaults, duplicates should be filtered out",
			customEndpoints: []string{
				manifests.KubeAPIServerService("").Name, // duplicate of base default
				"custom.endpoint.ibm.com",
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.ibm.com",
			},
			description: "Duplicates of base defaults in custom list should be filtered out",
		},
		{
			name: "When custom endpoints duplicate audit webhook service, duplicates should be filtered out",
			customEndpoints: []string{
				config.AuditWebhookService, // duplicate of base default
				"custom.endpoint.ibm.com",
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.ibm.com",
			},
			description: "Duplicates of audit webhook service should be filtered out",
		},
		{
			name: "When custom endpoints duplicate both IBM defaults, duplicates should be filtered out",
			customEndpoints: []string{
				"iam.cloud.ibm.com",      // duplicate
				"iam.test.cloud.ibm.com", // duplicate
				"custom.endpoint.ibm.com",
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"custom.endpoint.ibm.com",
			},
			description: "Duplicates of both IBM defaults should be filtered out",
		},
		{
			name: "When custom endpoints have multiple duplicates, all duplicates should be filtered out",
			customEndpoints: []string{
				"endpoint1.ibm.com",
				"endpoint2.ibm.com",
				"endpoint1.ibm.com", // duplicate
				"endpoint3.ibm.com",
				"endpoint2.ibm.com", // duplicate
				"endpoint1.ibm.com", // duplicate
			},
			expectedOAuthNoProxy: []string{
				manifests.KubeAPIServerService("").Name,
				config.AuditWebhookService,
				"iam.cloud.ibm.com",
				"iam.test.cloud.ibm.com",
				"endpoint1.ibm.com",
				"endpoint2.ibm.com",
				"endpoint3.ibm.com",
			},
			description: "Multiple duplicates should all be filtered out, keeping only first occurrence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
						IBMCloud: &hyperv1.IBMCloudPlatformSpec{
							OAuthNoProxyEndpoints: tt.customEndpoints,
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.test.com",
						Port: 6443,
					},
				},
			}

			releaseImageProvider := &mockReleaseImageProvider{}
			params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

			g := NewWithT(t)
			g.Expect(params.OAuthNoProxy).To(Equal(tt.expectedOAuthNoProxy), tt.description)
		})
	}
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxyEmptyStrings(t *testing.T) {
	// Test that empty strings in custom endpoints are filtered out
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					OAuthNoProxyEndpoints: []string{
						"",
						"custom.endpoint.ibm.com",
						"",
						"another.endpoint.ibm.com",
						"",
					},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: "api.test.com",
				Port: 6443,
			},
		},
	}

	releaseImageProvider := &mockReleaseImageProvider{}
	params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

	// Empty strings should be filtered out
	expectedOAuthNoProxy := []string{
		manifests.KubeAPIServerService("").Name,
		config.AuditWebhookService,
		"iam.cloud.ibm.com",
		"iam.test.cloud.ibm.com",
		"custom.endpoint.ibm.com",
		"another.endpoint.ibm.com",
	}

	g := NewWithT(t)
	g.Expect(params.OAuthNoProxy).To(Equal(expectedOAuthNoProxy))
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxySpecialCharacters(t *testing.T) {
	// Test that special characters in endpoints are preserved and deduplication works correctly
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					OAuthNoProxyEndpoints: []string{
						"*.ibm.com",
						".ibm.com",
						"192.168.1.0/24",
						"[::1]",
						"*.ibm.com", // duplicate
						"endpoint-with-dash.ibm.com",
						"endpoint_with_underscore.ibm.com",
					},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: "api.test.com",
				Port: 6443,
			},
		},
	}

	releaseImageProvider := &mockReleaseImageProvider{}
	params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

	expectedOAuthNoProxy := []string{
		manifests.KubeAPIServerService("").Name,
		config.AuditWebhookService,
		"iam.cloud.ibm.com",
		"iam.test.cloud.ibm.com",
		"*.ibm.com",
		".ibm.com",
		"192.168.1.0/24",
		"[::1]",
		"endpoint-with-dash.ibm.com",
		"endpoint_with_underscore.ibm.com",
	}

	g := NewWithT(t)
	g.Expect(params.OAuthNoProxy).To(Equal(expectedOAuthNoProxy))
}

func TestNewOAuthServerParams_IBMCloudOAuthNoProxyMixedDuplicatesAndEmpty(t *testing.T) {
	// Test combination of duplicates and empty strings
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					OAuthNoProxyEndpoints: []string{
						"",
						"endpoint1.ibm.com",
						"",
						"endpoint1.ibm.com", // duplicate
						"endpoint2.ibm.com",
						"",
						"iam.cloud.ibm.com", // duplicate of default
					},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: "api.test.com",
				Port: 6443,
			},
		},
	}

	releaseImageProvider := &mockReleaseImageProvider{}
	params := NewOAuthServerParams(hcp, releaseImageProvider, "oauth.test.com", 443, false)

	// Both empty strings and duplicates should be filtered out
	expectedOAuthNoProxy := []string{
		manifests.KubeAPIServerService("").Name,
		config.AuditWebhookService,
		"iam.cloud.ibm.com",
		"iam.test.cloud.ibm.com",
		"endpoint1.ibm.com",
		"endpoint2.ibm.com",
	}

	g := NewWithT(t)
	g.Expect(params.OAuthNoProxy).To(Equal(expectedOAuthNoProxy))
}

func TestAppendUnique(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		toAdd    []string
		expected []string
	}{
		{
			name:     "When adding to empty slice, all non-empty items should be added",
			existing: []string{},
			toAdd:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding items with no duplicates, all should be appended",
			existing: []string{"a", "b"},
			toAdd:    []string{"c", "d"},
			expected: []string{"a", "b", "c", "d"},
		},
		{
			name:     "When adding items with duplicates, only unique items should be appended",
			existing: []string{"a", "b", "c"},
			toAdd:    []string{"b", "d", "c", "e"},
			expected: []string{"a", "b", "c", "d", "e"},
		},
		{
			name:     "When adding all duplicates, nothing should be appended",
			existing: []string{"a", "b", "c"},
			toAdd:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding empty slice, existing should remain unchanged",
			existing: []string{"a", "b"},
			toAdd:    []string{},
			expected: []string{"a", "b"},
		},
		{
			name:     "When both slices are empty, result should be empty",
			existing: []string{},
			toAdd:    []string{},
			expected: []string{},
		},
		{
			name:     "When adding items with empty strings, empty strings should be filtered out",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "c", ""},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "When adding only empty strings, nothing should be appended",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "", ""},
			expected: []string{"a", "b"},
		},
		{
			name:     "When adding items with special characters, deduplication should work",
			existing: []string{"*.example.com", ".example.com"},
			toAdd:    []string{"*.example.com", "192.168.1.0/24", ".example.com"},
			expected: []string{"*.example.com", ".example.com", "192.168.1.0/24"},
		},
		{
			name:     "When adding mix of empty, duplicates, and unique items, only unique non-empty should be added",
			existing: []string{"a", "b"},
			toAdd:    []string{"", "a", "c", "", "b", "d", ""},
			expected: []string{"a", "b", "c", "d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := appendUnique(tt.existing, tt.toAdd)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
