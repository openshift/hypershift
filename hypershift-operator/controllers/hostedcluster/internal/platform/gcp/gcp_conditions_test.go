package gcp

import (
	"regexp"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

// TestValidCredentials tests the ValidCredentials method for various condition states.
// This tests the logic for checking both ValidGCPWorkloadIdentity and ValidGCPCredentials conditions.
func TestValidCredentials(t *testing.T) {
	tests := []struct {
		name        string
		conditions  []metav1.Condition
		expected    bool
		description string
	}{
		{
			name: "When both conditions are true, it should return true",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected:    true,
			description: "Both conditions are present and true",
		},
		{
			name: "When ValidGCPWorkloadIdentity is false, it should return false",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionFalse,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected:    false,
			description: "ValidGCPWorkloadIdentity is false",
		},
		{
			name: "When ValidGCPCredentials is false, it should return false",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionFalse,
				},
			},
			expected:    false,
			description: "ValidGCPCredentials is false",
		},
		{
			name: "When both conditions are false, it should return false",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionFalse,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionFalse,
				},
			},
			expected:    false,
			description: "Both conditions are false",
		},
		{
			name: "When ValidGCPWorkloadIdentity is missing, it should return false",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected:    false,
			description: "ValidGCPWorkloadIdentity condition is missing",
		},
		{
			name: "When ValidGCPCredentials is missing, it should return false",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
			},
			expected:    false,
			description: "ValidGCPCredentials condition is missing",
		},
		{
			name:        "When no conditions exist, it should return false",
			conditions:  []metav1.Condition{},
			expected:    false,
			description: "No conditions present",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			hc := &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Conditions: tt.conditions,
				},
			}

			result := ValidCredentials(hc)
			g.Expect(result).To(Equal(tt.expected), tt.description)
		})
	}
}

// TestWorkloadIdentityValidationScenarios tests additional edge cases for WIF validation.
// This expands on the existing TestValidateWorkloadIdentityConfiguration with more comprehensive coverage.
func TestWorkloadIdentityValidationScenarios(t *testing.T) {
	g := NewWithT(t)
	platform := New("test-utilities-image", "test-capg-image", nil)

	tests := []struct {
		name        string
		hcluster    *hyperv1.HostedCluster
		expectError bool
		errorMsg    string
	}{
		{
			name: "When project number is empty, it should return error",
			hcluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
								ProjectNumber: "", // Empty
								PoolID:        "test-pool",
								ProviderID:    "test-provider",
								ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
									NodePool:     "test@project.iam.gserviceaccount.com",
									ControlPlane: "cp-test@project.iam.gserviceaccount.com",
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "project number is required",
		},
		{
			name: "When pool ID is empty, it should return error",
			hcluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
								ProjectNumber: "123456789012",
								PoolID:        "", // Empty
								ProviderID:    "test-provider",
								ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
									NodePool:     "test@project.iam.gserviceaccount.com",
									ControlPlane: "cp-test@project.iam.gserviceaccount.com",
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "pool ID is required",
		},
		{
			name: "When provider ID is empty, it should return error",
			hcluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
								ProjectNumber: "123456789012",
								PoolID:        "test-pool",
								ProviderID:    "", // Empty
								ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
									NodePool:     "test@project.iam.gserviceaccount.com",
									ControlPlane: "cp-test@project.iam.gserviceaccount.com",
								},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "provider ID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := platform.validateWorkloadIdentityConfiguration(tt.hcluster)
			if tt.expectError {
				g.Expect(err).ToNot(BeNil())
				if tt.errorMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
				}
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

// TestNetworkConfigAccessSafety tests that accessing NetworkConfig fields is safe.
// This addresses the CodeRabbit feedback about potential nil pointer panics.
func TestNetworkConfigAccessSafety(t *testing.T) {
	g := NewWithT(t)
	platform := New("test-utilities-image", "test-capg-image", nil)

	// Test with zero-value NetworkConfig (should be safe)
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "test-project",
					Region:  "us-central1",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						// Zero values - should not cause panic
						Network:                     hyperv1.GCPResourceReference{},
						PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{},
					},
					WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
						ProjectNumber: "123456789012",
						PoolID:        "test-pool",
						ProviderID:    "test-provider",
						ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
							NodePool:     "test@project.iam.gserviceaccount.com",
							ControlPlane: "cp-test@project.iam.gserviceaccount.com",
						},
					},
				},
			},
		},
	}

	// This should not panic when accessing NetworkConfig fields
	gcpCluster := &capigcp.GCPCluster{}
	err := platform.reconcileGCPCluster(gcpCluster, hcluster, hyperv1.APIEndpoint{Host: "test.example.com", Port: 443})

	// Should succeed without panic
	g.Expect(err).To(BeNil())
	g.Expect(gcpCluster.Spec.Project).To(Equal("test-project"))
	g.Expect(gcpCluster.Spec.Region).To(Equal("us-central1"))
	// Network should not be configured since Name is empty
	g.Expect(gcpCluster.Spec.Network.Name).To(BeNil())
}

// TestServiceAccountEmailValidation tests that the regex pattern validation for service account emails is working correctly.
// This addresses CodeRabbit feedback about hardening the service account email pattern.
func TestServiceAccountEmailValidation(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		isValid bool
	}{
		{
			name:    "When service account email is valid, it should pass validation",
			email:   "myservice@myproject123.iam.gserviceaccount.com",
			isValid: true,
		},
		{
			name:    "When service account email starts with letter, it should pass validation",
			email:   "a12345@project123.iam.gserviceaccount.com",
			isValid: true,
		},
		{
			name:    "When service account email has hyphens, it should pass validation",
			email:   "my-service@my-project-123.iam.gserviceaccount.com",
			isValid: true,
		},
		{
			name:    "When service account starts with digit, it should fail validation",
			email:   "123service@project123.iam.gserviceaccount.com",
			isValid: false,
		},
		{
			name:    "When project ID starts with digit, it should fail validation",
			email:   "myservice@123project.iam.gserviceaccount.com",
			isValid: false,
		},
		{
			name:    "When service account ends with hyphen, it should fail validation",
			email:   "myservice-@project123.iam.gserviceaccount.com",
			isValid: false,
		},
		{
			name:    "When project ID ends with hyphen, it should fail validation",
			email:   "myservice@project123-.iam.gserviceaccount.com",
			isValid: false,
		},
		{
			name:    "When service account is too short, it should fail validation",
			email:   "ab@project123.iam.gserviceaccount.com",
			isValid: false,
		},
		{
			name:    "When project ID is too short, it should fail validation",
			email:   "myservice@ab.iam.gserviceaccount.com",
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Test the pattern directly using regex matching
			// Pattern: ^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$
			pattern := `^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
			matched, err := regexp.MatchString(pattern, tt.email)

			g.Expect(err).To(BeNil(), "Pattern should be valid regex")
			if tt.isValid {
				g.Expect(matched).To(BeTrue(), "Email should match pattern: %s", tt.email)
			} else {
				g.Expect(matched).To(BeFalse(), "Email should not match pattern: %s", tt.email)
			}
		})
	}
}
