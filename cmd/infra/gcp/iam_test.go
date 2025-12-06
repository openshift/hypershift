package gcp

import (
	"testing"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
)

func TestIAMManagerFormatServiceAccountMethods(t *testing.T) {
	manager := &IAMManager{
		projectID: "test-project",
		infraID:   "test-infra",
		logger:    logr.Discard(),
	}

	tests := []struct {
		name     string
		method   func(string) string
		arg      string
		expected string
	}{
		{
			name:     "When formatServiceAccountID is called it should return correct ID",
			method:   manager.formatServiceAccountID,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt",
		},
		{
			name:     "When formatServiceAccountEmail is called it should return correct email",
			method:   manager.formatServiceAccountEmail,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountResource is called it should return correct resource path",
			method:   manager.formatServiceAccountResource,
			arg:      "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
			expected: "projects/test-project/serviceAccounts/test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountMember is called it should return correct member format",
			method:   manager.formatServiceAccountMember,
			arg:      "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
			expected: "serviceAccount:test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.method(tt.arg)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIAMManagerFormatWIFPrincipal(t *testing.T) {
	manager := &IAMManager{
		projectNumber: "123456789",
		infraID:       "test-infra",
		logger:        logr.Discard(),
	}

	tests := []struct {
		name      string
		namespace string
		saName    string
		expected  string
	}{
		{
			name:      "When formatWIFPrincipal is called with kube-system namespace it should return correct principal",
			namespace: "kube-system",
			saName:    "control-plane-operator",
			expected:  "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-infra-wi-pool/subject/system:serviceaccount:kube-system:control-plane-operator",
		},
		{
			name:      "When formatWIFPrincipal is called with custom namespace it should return correct principal",
			namespace: "openshift-cloud-controller-manager",
			saName:    "cloud-controller-manager",
			expected:  "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-infra-wi-pool/subject/system:serviceaccount:openshift-cloud-controller-manager:cloud-controller-manager",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manager.formatWIFPrincipal(tt.namespace, tt.saName)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIAMManagerFormatIssuerUri(t *testing.T) {
	tests := []struct {
		name          string
		oidcIssuerURL string
		infraID       string
		expected      string
	}{
		{
			name:          "When custom OIDC issuer URL is set it should return the custom URL",
			oidcIssuerURL: "https://custom-oidc.example.com",
			infraID:       "test-infra",
			expected:      "https://custom-oidc.example.com",
		},
		{
			name:          "When no custom OIDC issuer URL is set it should derive from infraID",
			oidcIssuerURL: "",
			infraID:       "test-infra",
			expected:      "https://hypershift-test-infra-oidc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &IAMManager{
				oidcIssuerURL: tt.oidcIssuerURL,
				infraID:       tt.infraID,
				logger:        logr.Discard(),
			}

			got := manager.formatIssuerUri()
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAddMemberToRoleBinding(t *testing.T) {
	manager := &IAMManager{
		logger: logr.Discard(),
	}

	tests := []struct {
		name           string
		policy         *cloudresourcemanager.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member does not exist in role it should add member and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:new@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  2,
		},
		{
			name: "When member already exists in role it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:existing@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should create new binding and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:new@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manager.addMemberToRoleBinding(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected result %v, got %v", tt.expectedResult, got)
			}

			// Verify member count in the role binding
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestRemoveMemberFromRoleBinding(t *testing.T) {
	manager := &IAMManager{
		logger: logr.Discard(),
	}

	tests := []struct {
		name           string
		policy         *cloudresourcemanager.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member exists in role it should remove member and return true",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role: "roles/compute.admin",
						Members: []string{
							"serviceAccount:keep@project.iam.gserviceaccount.com",
							"serviceAccount:remove@project.iam.gserviceaccount.com",
						},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:remove@project.iam.gserviceaccount.com",
			expectedResult: true,
			expectedCount:  1,
		},
		{
			name: "When member does not exist in role it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{
					{
						Role:    "roles/compute.admin",
						Members: []string{"serviceAccount:existing@project.iam.gserviceaccount.com"},
					},
				},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:nonexistent@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should return false",
			policy: &cloudresourcemanager.Policy{
				Bindings: []*cloudresourcemanager.Binding{},
			},
			role:           "roles/compute.admin",
			member:         "serviceAccount:any@project.iam.gserviceaccount.com",
			expectedResult: false,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manager.removeMemberFromRoleBinding(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected result %v, got %v", tt.expectedResult, got)
			}

			// Verify member count in the role binding
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestAddMemberToServiceAccountRoleBinding(t *testing.T) {
	manager := &IAMManager{
		logger: logr.Discard(),
	}

	tests := []struct {
		name           string
		policy         *iam.Policy
		role           string
		member         string
		expectedResult bool
		expectedCount  int
	}{
		{
			name: "When member does not exist in role it should add member and return true",
			policy: &iam.Policy{
				Bindings: []*iam.Binding{
					{
						Role:    "roles/iam.workloadIdentityUser",
						Members: []string{"principal://existing"},
					},
				},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://new",
			expectedResult: true,
			expectedCount:  2,
		},
		{
			name: "When member already exists in role it should return false",
			policy: &iam.Policy{
				Bindings: []*iam.Binding{
					{
						Role:    "roles/iam.workloadIdentityUser",
						Members: []string{"principal://existing"},
					},
				},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://existing",
			expectedResult: false,
			expectedCount:  1,
		},
		{
			name: "When role does not exist it should create new binding and return true",
			policy: &iam.Policy{
				Bindings: []*iam.Binding{},
			},
			role:           "roles/iam.workloadIdentityUser",
			member:         "principal://new",
			expectedResult: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manager.addMemberToServiceAccountRoleBinding(tt.policy, tt.role, tt.member)
			if got != tt.expectedResult {
				t.Errorf("expected result %v, got %v", tt.expectedResult, got)
			}

			// Verify member count in the role binding
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					if len(binding.Members) != tt.expectedCount {
						t.Errorf("expected %d members, got %d", tt.expectedCount, len(binding.Members))
					}
					break
				}
			}
		})
	}
}

func TestLoadServiceAccountDefinitions(t *testing.T) {
	// Test loading the embedded default configuration
	t.Run("When loading embedded default configuration it should return valid definitions", func(t *testing.T) {
		definitions, err := loadServiceAccountDefinitions("")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(definitions) == 0 {
			t.Error("expected at least one service account definition")
		}

		// Verify each definition has required fields
		for _, def := range definitions {
			if def.Name == "" {
				t.Error("expected Name to be non-empty")
			}
			if def.DisplayName == "" {
				t.Errorf("expected DisplayName to be non-empty for %s", def.Name)
			}
		}
	})
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil it should return false",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsError(tt.err)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
