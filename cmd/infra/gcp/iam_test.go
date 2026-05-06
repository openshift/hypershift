package gcp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
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
			g := NewWithT(t)
			g.Expect(tt.method(tt.arg)).To(Equal(tt.expected))
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
			g := NewWithT(t)
			g.Expect(manager.formatWIFPrincipal(tt.namespace, tt.saName)).To(Equal(tt.expected))
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
			g := NewWithT(t)
			manager := &IAMManager{
				oidcIssuerURL: tt.oidcIssuerURL,
				infraID:       tt.infraID,
				logger:        logr.Discard(),
			}
			g.Expect(manager.formatIssuerUri()).To(Equal(tt.expected))
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
			g := NewWithT(t)
			g.Expect(manager.addMemberToRoleBinding(tt.policy, tt.role, tt.member)).To(Equal(tt.expectedResult))
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					g.Expect(binding.Members).To(HaveLen(tt.expectedCount))
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
			g := NewWithT(t)
			g.Expect(manager.removeMemberFromRoleBinding(tt.policy, tt.role, tt.member)).To(Equal(tt.expectedResult))
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					g.Expect(binding.Members).To(HaveLen(tt.expectedCount))
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
			g := NewWithT(t)
			g.Expect(manager.addMemberToServiceAccountRoleBinding(tt.policy, tt.role, tt.member)).To(Equal(tt.expectedResult))
			for _, binding := range tt.policy.Bindings {
				if binding.Role == tt.role {
					g.Expect(binding.Members).To(HaveLen(tt.expectedCount))
					break
				}
			}
		})
	}
}

func TestLoadServiceAccountDefinitions(t *testing.T) {
	t.Run("When loading embedded default configuration it should return valid definitions", func(t *testing.T) {
		g := NewWithT(t)
		definitions, err := loadServiceAccountDefinitions()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(definitions).NotTo(BeEmpty())

		for _, def := range definitions {
			g.Expect(def.Name).NotTo(BeEmpty())
			g.Expect(def.DisplayName).NotTo(BeEmpty(), "DisplayName should be non-empty for %s", def.Name)
		}
	})

	t.Run("When loading cloud-network definition it should have roles populated", func(t *testing.T) {
		g := NewWithT(t)
		definitions, err := loadServiceAccountDefinitions()
		g.Expect(err).NotTo(HaveOccurred())

		var cloudNetworkDef *ServiceAccountDefinition
		for i := range definitions {
			if definitions[i].Name == "cloud-network" {
				cloudNetworkDef = &definitions[i]
				break
			}
		}
		g.Expect(cloudNetworkDef).NotTo(BeNil(), "expected to find cloud-network service account definition")
		g.Expect(cloudNetworkDef.Roles).NotTo(BeEmpty())
	})

	t.Run("When loading image-registry definition it should have both operator and server K8s SAs", func(t *testing.T) {
		g := NewWithT(t)
		definitions, err := loadServiceAccountDefinitions()
		g.Expect(err).NotTo(HaveOccurred())

		var imageRegistryDef *ServiceAccountDefinition
		for i := range definitions {
			if definitions[i].Name == "image-registry" {
				imageRegistryDef = &definitions[i]
				break
			}
		}
		g.Expect(imageRegistryDef).NotTo(BeNil(), "expected to find image-registry service account definition")
		g.Expect(imageRegistryDef.K8sServiceAccounts).To(HaveLen(2), "image-registry should have 2 K8s SA bindings")
		g.Expect(imageRegistryDef.K8sServiceAccounts).To(ContainElements(
			K8sServiceAccountRef{Namespace: "openshift-image-registry", Name: "cluster-image-registry-operator"},
			K8sServiceAccountRef{Namespace: "openshift-image-registry", Name: "registry"},
		))
	})
}

func TestIsTransientIAMError(t *testing.T) {
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
		{
			name:     "When error is a 429 rate limit error it should return true",
			err:      &googleapi.Error{Code: 429, Message: "A quota has been reached"},
			expected: true,
		},
		{
			name:     "When error is a 404 not found error it should return true",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: true,
		},
		{
			name:     "When error is a 403 permission error it should return true",
			err:      &googleapi.Error{Code: 403, Message: "Permission denied"},
			expected: true,
		},
		{
			name:     "When error is a 403 non-permission error it should return false",
			err:      &googleapi.Error{Code: 403, Message: "Forbidden"},
			expected: false,
		},
		{
			name:     "When error is a 500 server error it should return false",
			err:      &googleapi.Error{Code: 500, Message: "Internal server error"},
			expected: false,
		},
		{
			name:     "When error is a non-googleapi error it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isTransientIAMError(tt.err)).To(Equal(tt.expected))
		})
	}
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
			g := NewWithT(t)
			g.Expect(isAlreadyExistsError(tt.err)).To(Equal(tt.expected))
		})
	}
}

func TestLoadAndValidateJWKS(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		setupFile     bool
		expectedError string
		expectedJSON  bool
	}{
		{
			name:         "When valid JWKS file is provided it should return the content",
			fileContent:  `{"keys": [{"kty": "RSA", "use": "sig", "kid": "test-key"}]}`,
			setupFile:    true,
			expectedJSON: true,
		},
		{
			name:          "When file does not exist it should return error",
			setupFile:     false,
			expectedError: "failed to read JWKS file",
		},
		{
			name:          "When file contains invalid JSON it should return error",
			fileContent:   `{not valid json}`,
			setupFile:     true,
			expectedError: "JWKS file contains invalid JSON",
		},
		{
			name:         "When file contains empty JSON object it should return it",
			fileContent:  `{}`,
			setupFile:    true,
			expectedJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var filePath string
			if tt.setupFile {
				tmpDir := t.TempDir()
				filePath = filepath.Join(tmpDir, "jwks.json")
				err := os.WriteFile(filePath, []byte(tt.fileContent), 0644)
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				filePath = filepath.Join(t.TempDir(), "non-existent.json")
			}

			result, err := loadAndValidateJWKS(filePath)

			if tt.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				if tt.expectedJSON {
					g.Expect(result).To(Equal(tt.fileContent))
				}
			}
		})
	}
}

func TestCompareJWKS(t *testing.T) {
	manager := &IAMManager{
		logger: logr.Discard(),
	}

	tests := []struct {
		name     string
		jwks1    string
		jwks2    string
		expected bool
	}{
		{
			name:     "When both are empty it should return true",
			jwks1:    "",
			jwks2:    "",
			expected: true,
		},
		{
			name:     "When both are whitespace-only it should return true",
			jwks1:    "  ",
			jwks2:    "  \t ",
			expected: true,
		},
		{
			name:     "When first is empty and second is not it should return false",
			jwks1:    "",
			jwks2:    `{"keys": []}`,
			expected: false,
		},
		{
			name:     "When first is non-empty and second is empty it should return false",
			jwks1:    `{"keys": []}`,
			jwks2:    "",
			expected: false,
		},
		{
			name:     "When both contain identical JSON it should return true",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "RSA"}]}`,
			expected: true,
		},
		{
			name:     "When both contain semantically equal JSON with different formatting it should return true",
			jwks1:    `{"keys":[{"kty":"RSA"}]}`,
			jwks2:    `{ "keys" : [ { "kty" : "RSA" } ] }`,
			expected: true,
		},
		{
			name:     "When JSON content differs it should return false",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "EC"}]}`,
			expected: false,
		},
		{
			name:     "When first contains invalid JSON it should return false",
			jwks1:    `{not json}`,
			jwks2:    `{"keys": []}`,
			expected: false,
		},
		{
			name:     "When second contains invalid JSON it should return false",
			jwks1:    `{"keys": []}`,
			jwks2:    `{not json}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			got := manager.compareJWKS(tt.jwks1, tt.jwks2)
			g.Expect(got).To(Equal(tt.expected))
		})
	}
}
