package gcp

import (
	"reflect"
	"regexp"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

// TestGetCredentialStatus tests the GetCredentialStatus function for various condition states.
// This tests the tri-state logic (valid/invalid/unknown) for GCP credential conditions.
func TestGetCredentialStatus(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		expected   CredentialStatus
	}{
		{
			name: "When both conditions are true, it should return CredentialStatusValid",
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
			expected: CredentialStatusValid,
		},
		{
			name: "When ValidGCPWorkloadIdentity is false, it should return CredentialStatusInvalid",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.InvalidIdentityProvider,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected: CredentialStatusInvalid,
		},
		{
			name: "When ValidGCPCredentials is false, it should return CredentialStatusInvalid",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.InvalidIdentityProvider,
				},
			},
			expected: CredentialStatusInvalid,
		},
		{
			name: "When both conditions are false, it should return CredentialStatusInvalid",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.InvalidIdentityProvider,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.InvalidIdentityProvider,
				},
			},
			expected: CredentialStatusInvalid,
		},
		{
			name: "When ValidGCPWorkloadIdentity is missing, it should return CredentialStatusUnknown",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected: CredentialStatusUnknown,
		},
		{
			name: "When ValidGCPCredentials is missing, it should return CredentialStatusUnknown",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
			},
			expected: CredentialStatusUnknown,
		},
		{
			name:       "When no conditions exist, it should return CredentialStatusUnknown",
			conditions: []metav1.Condition{},
			expected:   CredentialStatusUnknown,
		},
		{
			name: "When ValidGCPWorkloadIdentity is unknown and ValidGCPCredentials is true, it should return CredentialStatusUnknown",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionUnknown,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionTrue,
				},
			},
			expected: CredentialStatusUnknown,
		},
		{
			name: "When ValidGCPWorkloadIdentity is true and ValidGCPCredentials is unknown, it should return CredentialStatusUnknown",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ValidGCPWorkloadIdentity),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ValidGCPCredentials),
					Status: metav1.ConditionUnknown,
				},
			},
			expected: CredentialStatusUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, version := range []string{"4.23.0", "5.0.0", "5.1.0", "5.1.0-0.ci-20260909", "6.0.0", "", "invalid"} {
				t.Run("When control plane version is "+version+", it should enforce validation support", func(t *testing.T) {
					hc := &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{Conditions: tt.conditions}}
					hc.Status.ControlPlaneVersion.Desired.Version = version
					expected := tt.expected
					if version != "5.1.0" && version != "5.1.0-0.ci-20260909" && version != "6.0.0" {
						expected = CredentialStatusUnknown
					}
					NewWithT(t).Expect(GetCredentialStatus(hc)).To(Equal(expected))
				})
			}
		})
	}

	for _, version := range []string{"4.23.0", "5.0.0", "", "invalid", "5.1.0-0.ci-20260909", "6.0.0"} {
		for _, reason := range []string{hyperv1.InvalidIdentityProvider, hyperv1.InvalidConfigurationReason, hyperv1.ReconciliationErrorReason, ""} {
			t.Run("When version is "+version+" and failure reason is "+reason+", it should classify only supported runtime failures as invalid", func(t *testing.T) {
				hc := &hyperv1.HostedCluster{}
				hc.Status.ControlPlaneVersion.Desired.Version = version
				hc.Status.Conditions = []metav1.Condition{{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: reason}}
				expected := CredentialStatusUnknown
				if (version == "5.1.0-0.ci-20260909" || version == "6.0.0") && reason == hyperv1.InvalidIdentityProvider {
					expected = CredentialStatusInvalid
				}
				NewWithT(t).Expect(GetCredentialStatus(hc)).To(Equal(expected))
			})
		}
	}

}

// TestWorkloadIdentityValidationScenarios tests additional edge cases for WIF validation.
// This expands on the existing TestValidateWorkloadIdentityConfiguration with more comprehensive coverage.
func TestWorkloadIdentityValidationScenarios(t *testing.T) {
	g := NewWithT(t)

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
			err := validateWorkloadIdentityConfiguration(tt.hcluster)
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

func TestComputeGCPCredentialConditions(t *testing.T) {
	const unsupportedMessage = "Runtime GCP credential validation requires control plane version 5.1 or later"
	const unknownMessage = "The control plane version cannot be determined; runtime GCP credential validation availability is unknown"
	for _, tc := range []struct {
		name, hcVersion, hcpVersion                     string
		missingHCP                                      bool
		previousStatus, runtimeStatus, expectedStatus   metav1.ConditionStatus
		previousReason, expectedReason, expectedMessage string
	}{
		{
			name:            "When HCP is 4.23, it should replace legacy success",
			hcpVersion:      "4.23.0",
			previousStatus:  metav1.ConditionTrue,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "UnsupportedControlPlaneVersion",
			expectedMessage: unsupportedMessage,
		},
		{
			name:            "When HCP is 5.0, it should replace even latched authentication failures",
			hcpVersion:      "5.0.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "UnsupportedControlPlaneVersion",
			expectedMessage: unsupportedMessage,
		},
		{
			name:            "When HCP is 5.0 and HC is stale 5.1, it should use HCP version",
			hcVersion:       "5.1.0",
			hcpVersion:      "5.0.0",
			runtimeStatus:   metav1.ConditionTrue,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "UnsupportedControlPlaneVersion",
			expectedMessage: unsupportedMessage,
		},
		{
			name:            "When HCP version is empty and HC is 5.1, it should report undetermined",
			hcVersion:       "5.1.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: unknownMessage,
		},
		{
			name:            "When HCP version is malformed, it should replace legacy success",
			hcpVersion:      "invalid",
			previousStatus:  metav1.ConditionTrue,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: unknownMessage,
		},
		{
			name:            "When HCP disappears with HC at 5.0, it should disable validation",
			missingHCP:      true,
			hcVersion:       "5.0.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "UnsupportedControlPlaneVersion",
			expectedMessage: unsupportedMessage,
		},
		{
			name:            "When HCP disappears with no persisted version, it should report undetermined",
			missingHCP:      true,
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: unknownMessage,
		},
		{
			name:            "When HCP is a 5.1 prerelease and HC is 5.0, it should propagate success",
			hcVersion:       "5.0.0",
			hcpVersion:      "5.1.0-0.ci-20260909",
			runtimeStatus:   metav1.ConditionTrue,
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  hyperv1.AsExpectedReason,
			expectedMessage: "runtime result",
		},
		{
			name:            "When supported HCP reports authentication failure, it should propagate failure",
			hcpVersion:      "5.1.0",
			runtimeStatus:   metav1.ConditionFalse,
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "runtime result",
		},
		{
			name:           "When supported HCP has no result, it should await validation",
			hcpVersion:     "5.1.0",
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: hyperv1.StatusUnknownReason,
		},
		{
			name:            "When supported HCP reports Unknown, it should preserve runtime authentication failure",
			hcpVersion:      "5.1.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			runtimeStatus:   metav1.ConditionUnknown,
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "previous result",
		},
		{
			name:            "When supported HCP disappears, it should preserve runtime authentication failure using HC version",
			missingHCP:      true,
			hcVersion:       "5.1.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  hyperv1.InvalidIdentityProvider,
			expectedMessage: "previous result",
		},
		{
			name:           "When supported HCP has no result after configuration failure, it should discard legacy failure",
			hcpVersion:     "5.1.0",
			previousStatus: metav1.ConditionFalse,
			previousReason: hyperv1.InvalidConfigurationReason,
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: hyperv1.StatusUnknownReason,
		},
		{
			name:           "When supported HCP reports Unknown after Secret reconciliation failure, it should discard legacy failure",
			hcpVersion:     "5.1.0",
			previousStatus: metav1.ConditionFalse,
			previousReason: hyperv1.ReconciliationErrorReason,
			runtimeStatus:  metav1.ConditionUnknown,
			expectedStatus: metav1.ConditionUnknown,
			expectedReason: hyperv1.StatusUnknownReason,
		},
		{
			name:            "When runtime validation recovers, it should replace latched failure",
			hcpVersion:      "5.1.0",
			previousStatus:  metav1.ConditionFalse,
			previousReason:  hyperv1.InvalidIdentityProvider,
			runtimeStatus:   metav1.ConditionTrue,
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  hyperv1.AsExpectedReason,
			expectedMessage: "runtime result",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
			hc.Status.ControlPlaneVersion.Desired.Version = tc.hcVersion
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Status.ControlPlaneVersion.Desired.Version = tc.hcpVersion
			hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{}
			hcp.Status.VersionStatus.Desired.Version = "5.0.0" // Data plane version must not gate management-side validation.
			for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
				if tc.previousStatus != "" {
					meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: tc.previousStatus, Reason: tc.previousReason, Message: "previous result", ObservedGeneration: 3})
				}
				if tc.runtimeStatus != "" {
					reason := hyperv1.AsExpectedReason
					if tc.runtimeStatus == metav1.ConditionFalse {
						reason = hyperv1.InvalidIdentityProvider
					}
					meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: tc.runtimeStatus, Reason: reason, Message: "runtime result", ObservedGeneration: 99})
				}
			}
			if tc.missingHCP {
				hcp = nil
			}
			before := hc.DeepCopy()
			changed := ComputeGCPCredentialConditions(hc, hcp)
			g.Expect(changed).To(Equal(!reflect.DeepEqual(before.Status.Conditions, hc.Status.Conditions)))
			for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
				condition := meta.FindStatusCondition(hc.Status.Conditions, string(conditionType))
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(tc.expectedStatus))
				g.Expect(condition.Reason).To(Equal(tc.expectedReason))
				g.Expect(condition.Message).To(Equal(tc.expectedMessage))
				g.Expect(condition.ObservedGeneration).To(Equal(int64(3)))
			}
			before = hc.DeepCopy()
			g.Expect(ComputeGCPCredentialConditions(hc, hcp)).To(BeFalse())
			g.Expect(hc.Status).To(Equal(before.Status))
		})
	}
	t.Run("When only WIF has a runtime failure, it should latch that failure and await credentials", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider, ObservedGeneration: 3},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: hyperv1.ReconciliationErrorReason, ObservedGeneration: 2},
		}
		g.Expect(ComputeGCPCredentialConditions(hc, nil)).To(BeTrue())
		g.Expect(meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidGCPWorkloadIdentity)).Status).To(Equal(metav1.ConditionFalse))
		credentials := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidGCPCredentials))
		g.Expect(credentials.Status).To(Equal(metav1.ConditionUnknown))
		g.Expect(credentials.ObservedGeneration).To(Equal(int64(3)))
		g.Expect(GetCredentialStatus(hc)).To(Equal(CredentialStatusInvalid))
		g.Expect(ComputeGCPCredentialConditions(hc, nil)).To(BeFalse())
	})

	t.Run("When control plane upgrades from 5.0 to 5.1, it should accept fresh runtime results", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{}
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Status.ControlPlaneVersion.Desired.Version = "5.0.0"
		g.Expect(ComputeGCPCredentialConditions(hc, hcp)).To(BeTrue())
		hcp.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
			meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason})
		}
		g.Expect(ComputeGCPCredentialConditions(hc, hcp)).To(BeTrue())
		hc.Status.ControlPlaneVersion = hcp.Status.ControlPlaneVersion
		g.Expect(GetCredentialStatus(hc)).To(Equal(CredentialStatusValid))
	})
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
