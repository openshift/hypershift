package gcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

// timeoutError simulates net/http's unexported tlsHandshakeTimeoutError.
type timeoutError string

func (e timeoutError) Error() string   { return string(e) }
func (e timeoutError) Timeout() bool   { return true }
func (e timeoutError) Temporary() bool { return true }

func invalidIAMReferences() []struct {
	name, project, projectNumber, pool, provider, errorText string
} {
	return []struct{ name, project, projectNumber, pool, provider, errorText string }{
		{"When project is missing, it should reject references before creating clients", "", "987654321", "pool", "provider", "project-id is required"},
		{"When the WIF project number is missing, it should reject references before creating clients", "project", "", "pool", "provider", "workload identity project number is required"},
		{"When the WIF project number is a project ID, it should reject references before creating clients", "project", "other-project", "pool", "provider", "must contain only digits"},
		{"When the WIF project number contains whitespace, it should reject references before creating clients", "project", " 987654321", "pool", "provider", "must contain only digits"},
		{"When pool is missing, it should reject references before creating clients", "project", "987654321", "", "provider", "workload identity pool ID is required"},
		{"When provider is missing, it should reject references before creating clients", "project", "987654321", "pool", "", "workload identity provider ID is required"},
		{"When accounts are missing, it should reject references before creating clients", "project", "987654321", "pool", "provider", "service account email for"},
	}
}

func TestValidateIAMResourceReferences(t *testing.T) {
	for _, test := range invalidIAMReferences() {
		t.Run(test.name, func(t *testing.T) {
			err := validateIAMResourceReferences(test.project, test.projectNumber, test.pool, test.provider, nil)
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected error containing %q, got %v", test.errorText, err)
			}
		})
	}
}

func TestNewIAMManagerWithResources(t *testing.T) {
	for _, test := range invalidIAMReferences() {
		t.Run(test.name, func(t *testing.T) {
			manager, err := NewIAMManagerWithResources(context.Background(), test.project, test.projectNumber, test.pool, test.provider, nil, logr.Discard())
			if manager != nil || err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("expected no manager and error containing %q, got %v, %v", test.errorText, manager, err)
			}
		})
	}
	t.Run("When explicit references are valid, it should keep the service-account and WIF projects separate", func(t *testing.T) {
		g := NewWithT(t)
		// Constructors read ADC configuration but do not fetch tokens. Use fake
		// credentials so this test never depends on the developer's environment.
		credentials := filepath.Join(t.TempDir(), "adc.json")
		g.Expect(os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"test-client","client_secret":"test-secret","refresh_token":"test-token"}`), 0600)).To(Succeed())
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentials)
		definitions, err := loadServiceAccountDefinitions()
		g.Expect(err).ToNot(HaveOccurred())
		emails := make(map[string]string, len(definitions))
		for _, definition := range definitions {
			emails[definition.Name] = definition.Name + "@test-project.iam.gserviceaccount.com"
		}
		manager, err := NewIAMManagerWithResources(context.Background(), "test-project", "987654321", "recorded-pool", "recorded-provider", emails, logr.Discard())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(manager.projectID).To(Equal("test-project"))
		g.Expect(manager.workloadIdentityProjectNumber).To(Equal("987654321"))
		g.Expect(manager.serviceAccountEmails).To(Equal(emails))
		g.Expect(manager.formatPoolParent()).To(Equal("projects/987654321/locations/global/workloadIdentityPools/recorded-pool"))
	})
}

func TestIAMManagerFormatServiceAccountMethods(t *testing.T) {
	manager := &IAMManager{
		projectID:                     "test-project",
		workloadIdentityProjectNumber: "987654321",
		infraID:                       "test-infra",
		logger:                        logr.Discard(),
	}

	tests := []struct {
		name     string
		method   func(string) string
		arg      string
		expected string
	}{
		{
			name:     "When formatServiceAccountID is called, it should return correct ID",
			method:   manager.formatServiceAccountID,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt",
		},
		{
			name:     "When formatServiceAccountEmail is called, it should return correct email",
			method:   manager.formatServiceAccountEmail,
			arg:      "nodepool-mgmt",
			expected: "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountResource is called, it should return correct resource path",
			method:   manager.formatServiceAccountResource,
			arg:      "test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
			expected: "projects/test-project/serviceAccounts/test-infra-nodepool-mgmt@test-project.iam.gserviceaccount.com",
		},
		{
			name:     "When formatServiceAccountMember is called, it should return correct member format",
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
	NewWithT(t).Expect(manager.formatProjectResource()).To(Equal("projects/test-project"))
}

func TestIAMManagerUsesExplicitHostedClusterReferences(t *testing.T) {
	manager := &IAMManager{
		projectID:            "test-project",
		infraID:              "generated-cluster-infra",
		workloadIdentityPool: "independent-pool",
		workloadIdentityOIDC: "independent-provider",
		serviceAccountEmails: map[string]string{
			"nodepool-mgmt": "custom-nodepool@test-project.iam.gserviceaccount.com",
		},
	}

	if got := manager.formatPoolID(); got != "independent-pool" {
		t.Errorf("formatPoolID() = %q, want independent-pool", got)
	}
	if got := manager.formatProviderID(); got != "independent-provider" {
		t.Errorf("formatProviderID() = %q, want independent-provider", got)
	}
	if got := manager.formatServiceAccountEmail("nodepool-mgmt"); got != "custom-nodepool@test-project.iam.gserviceaccount.com" {
		t.Errorf("formatServiceAccountEmail() = %q, want exact HostedCluster email", got)
	}
}

func iamCleanupProjects() []struct{ name, projectNumber, poolResource, providerID string } {
	return []struct{ name, projectNumber, poolResource, providerID string }{
		{"When WIF is in the HostedCluster project, it should use its recorded project number", "123456789", "projects/123456789/locations/global/workloadIdentityPools/recorded-pool", "recorded-provider"},
		{"When WIF is in another project, it should use the recorded WIF project number", "987654321", "projects/987654321/locations/global/workloadIdentityPools/recorded-pool", "recorded-provider"},
		{"When standalone cleanup has no WIF project number, it should keep the project ID and derived names", "", "projects/test-project/locations/global/workloadIdentityPools/test-infra-wi-pool", "test-infra-k8s-provider"},
	}
}

func iamCleanupManager(projectNumber string) *IAMManager {
	manager := &IAMManager{
		projectID: "test-project", infraID: "test-infra", logger: logr.Discard(),
		workloadIdentityProjectNumber: projectNumber,
	}
	if projectNumber != "" {
		manager.workloadIdentityPool = "recorded-pool"
		manager.workloadIdentityOIDC = "recorded-provider"
	}
	return manager
}

func TestIAMManagerFormatPoolParent(t *testing.T) {
	for _, test := range iamCleanupProjects() {
		t.Run(test.name, func(t *testing.T) {
			NewWithT(t).Expect(iamCleanupManager(test.projectNumber).formatPoolParent()).To(Equal(test.poolResource))
		})
	}
}

type iamCleanupTransport func(*http.Request) (*http.Response, error)

func (transport iamCleanupTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// assertIAMDeleteRequest exercises the real SDK request construction using an
// in-memory transport. No authentication or network access is performed.
func assertIAMDeleteRequest(t *testing.T, manager *IAMManager, destroy func(context.Context) error, resource string, asynchronous bool) {
	t.Helper()
	g := NewWithT(t)
	expected := []string{"DELETE /v1/" + resource}
	operation := resource + "/operations/delete"
	if asynchronous {
		expected = append(expected, "GET /v1/"+operation)
	}
	var requests []string
	transport := iamCleanupTransport(func(request *http.Request) (*http.Response, error) {
		call := request.Method + " " + request.URL.Path
		index := len(requests)
		requests = append(requests, call)
		if index >= len(expected) || call != expected[index] {
			return nil, fmt.Errorf("unexpected IAM cleanup request: %s", call)
		}
		body := `{}`
		if asynchronous {
			if request.Method == http.MethodDelete {
				body = fmt.Sprintf(`{"name":%q}`, operation)
			} else {
				body = `{"done":true}`
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	service, err := iam.NewService(context.Background(), option.WithHTTPClient(&http.Client{Transport: transport}))
	g.Expect(err).ToNot(HaveOccurred())
	manager.iamService = service
	g.Expect(destroy(context.Background())).To(Succeed())
	g.Expect(requests).To(Equal(expected))
}

func TestIAMManagerDeleteWorkloadIdentityPool(t *testing.T) {
	for _, test := range iamCleanupProjects() {
		t.Run(test.name, func(t *testing.T) {
			manager := iamCleanupManager(test.projectNumber)
			assertIAMDeleteRequest(t, manager, manager.DeleteWorkloadIdentityPool, test.poolResource, true)
		})
	}
}

func TestIAMManagerDeleteOIDCProvider(t *testing.T) {
	for _, test := range iamCleanupProjects() {
		t.Run(test.name, func(t *testing.T) {
			manager := iamCleanupManager(test.projectNumber)
			assertIAMDeleteRequest(t, manager, manager.DeleteOIDCProvider, test.poolResource+"/providers/"+test.providerID, true)
		})
	}
}

func TestIAMManagerDeleteServiceAccount(t *testing.T) {
	for _, test := range []struct{ name, projectNumber string }{
		{"When WIF is in the HostedCluster project, it should delete service accounts by project ID", "123456789"},
		{"When WIF is in another project, it should keep service-account deletion in the HostedCluster project", "987654321"},
		{"When standalone cleanup has no WIF project number, it should retain service-account deletion by project ID", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := iamCleanupManager(test.projectNumber)
			email := "custom-account@test-project.iam.gserviceaccount.com"
			assertIAMDeleteRequest(t, manager, func(ctx context.Context) error {
				return manager.deleteServiceAccount(ctx, email)
			}, "projects/test-project/serviceAccounts/"+email, false)
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
			name:      "When formatWIFPrincipal is called with kube-system namespace, it should return correct principal",
			namespace: "kube-system",
			saName:    "control-plane-operator",
			expected:  "principal://iam.googleapis.com/projects/123456789/locations/global/workloadIdentityPools/test-infra-wi-pool/subject/system:serviceaccount:kube-system:control-plane-operator",
		},
		{
			name:      "When formatWIFPrincipal is called with custom namespace, it should return correct principal",
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
			name:          "When custom OIDC issuer URL is set, it should return the custom URL",
			oidcIssuerURL: "https://custom-oidc.example.com",
			infraID:       "test-infra",
			expected:      "https://custom-oidc.example.com",
		},
		{
			name:          "When no custom OIDC issuer URL is set, it should derive from infraID",
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
			name:     "When error is nil, it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a 404 not found error, it should return true",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: true,
		},
		{
			name:     "When error is a 403 permission error, it should return true",
			err:      &googleapi.Error{Code: 403, Message: "Permission denied"},
			expected: true,
		},
		{
			name:     "When error is a 403 non-permission error, it should return false",
			err:      &googleapi.Error{Code: 403, Message: "Forbidden"},
			expected: false,
		},
		{
			name:     "When error is a 400 IAM error, it should return true",
			err:      &googleapi.Error{Code: 400, Message: "Service account does not exist"},
			expected: true,
		},
		{
			name:     "When error is a 400 non-IAM error, it should return false",
			err:      &googleapi.Error{Code: 400, Message: "Invalid argument"},
			expected: false,
		},
		{
			name:     "When error is a 429 rate limit error, it should return false",
			err:      &googleapi.Error{Code: 429, Message: "Rate limited"},
			expected: false,
		},
		{
			name:     "When error is a 500 server error, it should return false",
			err:      &googleapi.Error{Code: 500, Message: "Internal server error"},
			expected: false,
		},
		{
			name:     "When error is a non-googleapi error, it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
		{
			name:     "When error is a network error, it should return false",
			err:      &net.OpError{Op: "read", Net: "tcp", Err: fmt.Errorf("connection reset by peer")},
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

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil, it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a 429 rate limit error, it should return true",
			err:      &googleapi.Error{Code: 429, Message: "Rate limited"},
			expected: true,
		},
		{
			name:     "When error is a 500 server error, it should return true",
			err:      &googleapi.Error{Code: 500, Message: "Internal server error"},
			expected: true,
		},
		{
			name:     "When error is a 502 bad gateway error, it should return true",
			err:      &googleapi.Error{Code: 502, Message: "Bad gateway"},
			expected: true,
		},
		{
			name:     "When error is a 503 service unavailable error, it should return true",
			err:      &googleapi.Error{Code: 503, Message: "Service unavailable"},
			expected: true,
		},
		{
			name:     "When error is a 504 gateway timeout error, it should return true",
			err:      &googleapi.Error{Code: 504, Message: "Gateway timeout"},
			expected: true,
		},
		{
			name:     "When error is a transient network error, it should return true",
			err:      &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}},
			expected: true,
		},
		{
			name:     "When error is a 404 not found error, it should return false",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: false,
		},
		{
			name:     "When error is a non-retryable error, it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isTransientError(tt.err)).To(Equal(tt.expected))
		})
	}
}

func TestIsTransientNetworkError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil, it should return false",
			err:      nil,
			expected: false,
		},
		{
			name: "When error is a connection reset by peer, it should return true",
			err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET},
			},
			expected: true,
		},
		{
			name: "When error is network unreachable, it should return true",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ENETUNREACH},
			},
			expected: true,
		},
		{
			name:     "When error wraps a transient net.OpError, it should return true",
			err:      fmt.Errorf("failed to get IAM policy: %w", &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}),
			expected: true,
		},
		{
			name:     "When error is a DNS not found, it should return false",
			err:      &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true},
			expected: false,
		},
		{
			name:     "When error is a temporary DNS error, it should return true",
			err:      &net.DNSError{Err: "temporary failure", Name: "example.com", IsTemporary: true},
			expected: true,
		},
		{
			name:     "When error is a DNS timeout, it should return true",
			err:      &net.DNSError{Err: "timeout", Name: "example.com", IsTimeout: true},
			expected: true,
		},
		{
			name:     "When error is connection refused, it should return true",
			err:      &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}},
			expected: true,
		},
		{
			name:     "When error is host unreachable, it should return true",
			err:      &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EHOSTUNREACH}},
			expected: true,
		},
		{
			name:     "When error is a syscall timeout, it should return true",
			err:      &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ETIMEDOUT}},
			expected: true,
		},
		{
			name:     "When error is connection aborted, it should return true",
			err:      &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNABORTED}},
			expected: true,
		},
		{
			name:     "When error is a broken pipe, it should return true",
			err:      &net.OpError{Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}},
			expected: true,
		},
		{
			name:     "When error is io.ErrUnexpectedEOF, it should return true",
			err:      io.ErrUnexpectedEOF,
			expected: true,
		},
		{
			name:     "When error is io.EOF, it should return true",
			err:      io.EOF,
			expected: true,
		},
		{
			name:     "When error is a url.Error wrapping a transient error, it should return true",
			err:      &url.Error{Op: "Post", URL: "https://iam.googleapis.com", Err: &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}},
			expected: true,
		},
		{
			name:     "When error is a net.Error with Timeout, it should return true",
			err:      timeoutError("net/http: TLS handshake timeout"),
			expected: true,
		},
		{
			name:     "When error is an address error, it should return false",
			err:      &net.AddrError{Err: "invalid address", Addr: "not-a-host"},
			expected: false,
		},
		{
			name:     "When error is a non-network error, it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
		{
			name:     "When error is an IAM API error, it should return false",
			err:      &googleapi.Error{Code: 400, Message: "Service account does not exist"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isTransientNetworkError(tt.err)).To(Equal(tt.expected))
		})
	}
}

func TestIsRetryableIAMError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil, it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When error is a transient IAM error (404), it should return true",
			err:      &googleapi.Error{Code: 404, Message: "Not found"},
			expected: true,
		},
		{
			name:     "When error is a transient server error (429), it should return true",
			err:      &googleapi.Error{Code: 429, Message: "Rate limited"},
			expected: true,
		},
		{
			name:     "When error is a 504 gateway timeout, it should return true",
			err:      &googleapi.Error{Code: 504, Message: "Gateway timeout"},
			expected: true,
		},
		{
			name:     "When error is a transient network error, it should return true",
			err:      &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}},
			expected: true,
		},
		{
			name:     "When error is neither IAM nor network, it should return false",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isRetryableIAMError(tt.err)).To(Equal(tt.expected))
		})
	}
}

func TestRetryWithExponentialBackoff(t *testing.T) {
	manager := &IAMManager{logger: logr.Discard()}

	t.Run("When operation returns a non-retryable error, it should run once and propagate the error", func(t *testing.T) {
		g := NewWithT(t)
		calls := 0
		permanentErr := fmt.Errorf("permanent failure")

		err := manager.retryWithExponentialBackoff(context.Background(), "test-op", isTransientError, func() error {
			calls++
			return permanentErr
		})

		g.Expect(err).To(MatchError(permanentErr))
		g.Expect(calls).To(Equal(1))
	})

	t.Run("When operation returns a transient error then succeeds, it should retry and return nil", func(t *testing.T) {
		g := NewWithT(t)
		calls := 0
		transientErr := &net.OpError{
			Op:  "read",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET},
		}

		err := manager.retryWithExponentialBackoff(context.Background(), "test-op", isTransientError, func() error {
			calls++
			if calls == 1 {
				return transientErr
			}
			return nil
		})

		g.Expect(err).To(BeNil())
		g.Expect(calls).To(Equal(2))
	})

	t.Run("When context is canceled, it should stop retrying", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		transientErr := &googleapi.Error{Code: 429, Message: "Rate limited"}

		cancel()

		err := manager.retryWithExponentialBackoff(ctx, "test-op", isRetryableIAMError, func() error {
			calls++
			return transientErr
		})

		g.Expect(err).To(HaveOccurred())
		g.Expect(calls).To(Equal(1))
	})

	t.Run("When using isTransientError, a 404 should not be retried", func(t *testing.T) {
		g := NewWithT(t)
		calls := 0
		notFoundErr := &googleapi.Error{Code: 404, Message: "Project not found"}

		err := manager.retryWithExponentialBackoff(context.Background(), "test-op", isTransientError, func() error {
			calls++
			return notFoundErr
		})

		g.Expect(err).To(MatchError(notFoundErr))
		g.Expect(calls).To(Equal(1))
	})
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is nil, it should return false",
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
			name:     "When both are empty, it should return true",
			jwks1:    "",
			jwks2:    "",
			expected: true,
		},
		{
			name:     "When both are whitespace-only, it should return true",
			jwks1:    "  ",
			jwks2:    "  \t ",
			expected: true,
		},
		{
			name:     "When first is empty and second is not, it should return false",
			jwks1:    "",
			jwks2:    `{"keys": []}`,
			expected: false,
		},
		{
			name:     "When first is non-empty and second is empty, it should return false",
			jwks1:    `{"keys": []}`,
			jwks2:    "",
			expected: false,
		},
		{
			name:     "When both contain identical JSON, it should return true",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "RSA"}]}`,
			expected: true,
		},
		{
			name:     "When both contain semantically equal JSON with different formatting, it should return true",
			jwks1:    `{"keys":[{"kty":"RSA"}]}`,
			jwks2:    `{ "keys" : [ { "kty" : "RSA" } ] }`,
			expected: true,
		},
		{
			name:     "When JSON content differs, it should return false",
			jwks1:    `{"keys": [{"kty": "RSA"}]}`,
			jwks2:    `{"keys": [{"kty": "EC"}]}`,
			expected: false,
		},
		{
			name:     "When first contains invalid JSON, it should return false",
			jwks1:    `{not json}`,
			jwks2:    `{"keys": []}`,
			expected: false,
		},
		{
			name:     "When second contains invalid JSON, it should return false",
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
