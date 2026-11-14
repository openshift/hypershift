package azure

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/go-logr/logr"
)

// componentNames returns the ComponentName of each workload identity definition.
func componentNames(defs []WorkloadIdentityDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.ComponentName)
	}
	return names
}

// TestCreateIAMOptionsWorkloadIdentityPropagation verifies that DisableClusterCapabilities on the
// IAM create options propagates into the workload identity selection: disabling ImageRegistry skips
// its identity and federated credentials while retaining the others, and a non-registry disabled
// capability leaves the registry identity in place.
func TestCreateIAMOptions_WorkloadIdentityOptions(t *testing.T) {
	t.Parallel()

	alwaysPresent := []string{"disk", "file", "ingress", "cloudProvider", "nodePoolManagement", "network"}

	tests := map[string]struct {
		disabled       []string
		expectRegistry bool
	}{
		"When no capabilities are disabled it should include the image registry identity": {
			disabled:       nil,
			expectRegistry: true,
		},
		"When ImageRegistry is disabled it should skip the image registry identity": {
			disabled:       []string{string(hyperv1.ImageRegistryCapability)},
			expectRegistry: false,
		},
		"When a non-registry capability is disabled it should retain the image registry identity": {
			disabled:       []string{"Ingress"},
			expectRegistry: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			opts := &CreateIAMOptions{Name: "test-cluster", DisableClusterCapabilities: test.disabled}
			defs := GetWorkloadIdentityDefinitions(opts.Name, opts.workloadIdentityOptions())
			names := componentNames(defs)

			for _, required := range alwaysPresent {
				g.Expect(names).To(ContainElement(required), "expected %q identity to always be created", required)
			}

			if test.expectRegistry {
				g.Expect(names).To(ContainElement("imageRegistry"))
			} else {
				g.Expect(names).ToNot(ContainElement("imageRegistry"), "disabling ImageRegistry must skip its identity and federated credentials")
			}

			// Every created identity must retain its federated credentials.
			for _, def := range defs {
				g.Expect(def.FederatedCredentials).ToNot(BeEmpty(), "identity %q must keep its federated credentials", def.ComponentName)
			}
		})
	}
}

func TestNewIdentityManager(t *testing.T) {
	tests := map[string]struct {
		subscriptionID string
		cloud          string
	}{
		"When valid subscription ID is provided it should create identity manager": {
			subscriptionID: "12345678-1234-1234-1234-123456789012",
			cloud:          "AzurePublicCloud",
		},
		"When different subscription ID is provided it should create identity manager with that ID": {
			subscriptionID: "abcdefgh-abcd-abcd-abcd-abcdefghijkl",
			cloud:          "AzureUSGovernmentCloud",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create with nil credentials since we're just testing the constructor
			manager := NewIdentityManager(test.subscriptionID, nil, test.cloud)

			g.Expect(manager).ToNot(BeNil())
			g.Expect(manager.subscriptionID).To(Equal(test.subscriptionID))
			g.Expect(manager.creds).To(BeNil())
			g.Expect(manager.cloud).To(Equal(test.cloud))
		})
	}
}

func TestGetWorkloadIdentityDefinitions(t *testing.T) {
	tests := map[string]struct {
		clusterName       string
		opts              WorkloadIdentityOptions
		expectedCount     int
		expectedComponent []string
	}{
		"When public topology without KMS it should return 7 identity definitions": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Public"},
			expectedCount: 7,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
			},
		},
		"When ImageRegistry is disabled it should omit the registry identity definition": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Public", DisableImageRegistry: true},
			expectedCount: 6,
			expectedComponent: []string{
				"disk",
				"file",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
			},
		},
		"When private topology without KMS it should return 8 identity definitions with controlPlaneOperator": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Private"},
			expectedCount: 8,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"controlPlaneOperator",
			},
		},
		"When public topology with KMS it should return 8 identity definitions including kms": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Public", IncludeKMS: true},
			expectedCount: 8,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"kms",
			},
		},
		"When private topology with KMS it should return 9 identity definitions": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Private", IncludeKMS: true},
			expectedCount: 9,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"controlPlaneOperator",
				"kms",
			},
		},
		"When empty topology with KMS it should return 9 identity definitions for cleanup": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{IncludeKMS: true},
			expectedCount: 9,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"controlPlaneOperator",
				"kms",
			},
		},
		"When public topology with Karpenter it should return 8 identity definitions including karpenter": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{Topology: "Public", IncludeKarpenter: true},
			expectedCount: 8,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"karpenter",
			},
		},
		"When empty topology with KMS and Karpenter it should return 10 identity definitions for cleanup": {
			clusterName:   "test-cluster",
			opts:          WorkloadIdentityOptions{IncludeKMS: true, IncludeKarpenter: true},
			expectedCount: 10,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
				"controlPlaneOperator",
				"kms",
				"karpenter",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			definitions := GetWorkloadIdentityDefinitions(test.clusterName, test.opts)

			// Verify count
			g.Expect(definitions).To(HaveLen(test.expectedCount))

			// Verify all expected components are present
			componentNames := make([]string, len(definitions))
			for i, def := range definitions {
				componentNames[i] = def.ComponentName
			}

			for _, expectedComponent := range test.expectedComponent {
				g.Expect(componentNames).To(ContainElement(expectedComponent), "Should contain "+expectedComponent+" component")
			}

			// Verify each definition has at least one federated credential
			for _, def := range definitions {
				g.Expect(def.FederatedCredentials).ToNot(BeEmpty(), "Definition for %s should have at least one federated credential", def.ComponentName)
				g.Expect(def.IdentityNameSuffix).ToNot(BeEmpty(), "Definition for %s should have an identity name suffix", def.ComponentName)
			}
		})
	}
}

func TestKarpenterWorkloadIdentityDefinition(t *testing.T) {
	g := NewGomegaWithT(t)

	definitions := GetWorkloadIdentityDefinitions("test-cluster", WorkloadIdentityOptions{Topology: "Public", IncludeKarpenter: true})

	var karpenter *WorkloadIdentityDefinition
	for i := range definitions {
		if definitions[i].ComponentName == "karpenter" {
			karpenter = &definitions[i]
			break
		}
	}
	g.Expect(karpenter).ToNot(BeNil())
	g.Expect(karpenter.IdentityNameSuffix).To(Equal("-karpenter"))
	g.Expect(karpenter.FederatedCredentials).To(HaveLen(1))
	g.Expect(karpenter.FederatedCredentials[0].CredentialName).To(Equal("test-cluster-karpenter-fed-id"))
	g.Expect(karpenter.FederatedCredentials[0].Subject).To(Equal("system:serviceaccount:kube-system:karpenter"))
	g.Expect(karpenter.FederatedCredentials[0].Audience).To(Equal("openshift"))
}

func TestFederatedCredentialConfig(t *testing.T) {
	tests := map[string]struct {
		credentialName string
		subject        string
		audience       string
	}{
		"When disk driver node service account is configured it should store correct values": {
			credentialName: "test-disk-fed-id-node",
			subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
			audience:       "openshift",
		},
		"When ingress operator service account is configured it should store correct values": {
			credentialName: "test-ingress-fed-id",
			subject:        "system:serviceaccount:openshift-ingress-operator:ingress-operator",
			audience:       "openshift",
		},
		"When cloud provider service account is configured it should store correct values": {
			credentialName: "test-cloud-provider-fed-id",
			subject:        "system:serviceaccount:kube-system:azure-cloud-provider",
			audience:       "openshift",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			config := FederatedCredentialConfig{
				CredentialName: test.credentialName,
				Subject:        test.subject,
				Audience:       test.audience,
			}

			g.Expect(config.CredentialName).To(Equal(test.credentialName))
			g.Expect(config.Subject).To(Equal(test.subject))
			g.Expect(config.Audience).To(Equal(test.audience))
		})
	}
}

// TestCreateWorkloadIdentitiesFromIAMOptionsCreation exercises the actual creation path with a fake
// ARM transport and proves that disabling the ImageRegistry capability skips both the image registry
// managed identity and its federated credentials while still creating every other identity (and its
// federated credentials), and that disabling a non-registry capability does not suppress the registry.
func TestIdentityManager_CreateWorkloadIdentitiesFromIAMOptions(t *testing.T) {
	t.Parallel()

	// alwaysCreated are identity-name infixes (opts.Name + suffix + "-" + InfraID) expected regardless.
	alwaysCreated := []string{"-disk-", "-file-", "-ingress-", "-cloud-provider-", "-node-pool-mgmt-", "-network-"}

	tests := map[string]struct {
		disabled     []string
		wantRegistry bool
	}{
		"When no capability is disabled, it should create the image registry identity and its federated credentials": {
			disabled:     nil,
			wantRegistry: true,
		},
		"When ImageRegistry is disabled, it should skip the registry identity and its federated credentials but keep the others": {
			disabled:     []string{string(hyperv1.ImageRegistryCapability)},
			wantRegistry: false,
		},
		"When a non-registry capability is disabled, it should still create the image registry identity": {
			disabled:     []string{"Ingress"},
			wantRegistry: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			var identityPuts, federatedPuts []string
			transport := testHTTPTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPut {
					return nil, fmt.Errorf("unexpected method %s for %s", req.Method, req.URL.Path)
				}
				jsonResp := func(body string) *http.Response {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
						Request:    req,
					}
				}
				path := req.URL.Path
				if idx := strings.Index(path, "/federatedIdentityCredentials/"); idx != -1 {
					// Record the owning identity name (segment before /federatedIdentityCredentials/).
					owner := path[:idx]
					federatedPuts = append(federatedPuts, owner[strings.LastIndex(owner, "/")+1:])
					return jsonResp(`{}`), nil
				}
				identityName := path[strings.LastIndex(path, "/")+1:]
				identityPuts = append(identityPuts, identityName)
				return jsonResp(fmt.Sprintf(
					`{"id":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s","properties":{"clientId":"00000000-0000-0000-0000-000000000001","principalId":"00000000-0000-0000-0000-000000000002"}}`,
					identityName)), nil
			})

			mgr := NewIdentityManager("sub", testTokenCredential{}, "AzurePublicCloud")
			mgr.transport = transport

			opts := &CreateIAMOptions{
				Name:                       "test",
				InfraID:                    "infra",
				Location:                   "eastus",
				OIDCIssuerURL:              "https://oidc.example.com/test",
				DisableClusterCapabilities: tc.disabled,
			}

			out, err := mgr.CreateWorkloadIdentitiesFromIAMOptions(t.Context(), logr.Discard(), opts, "rg")
			g.Expect(err).ToNot(HaveOccurred())

			anyContains := func(items []string, infix string) bool {
				for _, it := range items {
					if strings.Contains(it, infix) {
						return true
					}
				}
				return false
			}

			// Every non-registry identity (and its federated credentials) must always be created.
			for _, infix := range alwaysCreated {
				g.Expect(anyContains(identityPuts, infix)).To(BeTrue(), "identity %q must be created", infix)
				g.Expect(anyContains(federatedPuts, infix)).To(BeTrue(), "federated credentials for %q must be created", infix)
			}

			registryIdentityCreated := anyContains(identityPuts, "-image-registry-")
			registryFederatedCreated := anyContains(federatedPuts, "-image-registry-")
			if tc.wantRegistry {
				g.Expect(registryIdentityCreated).To(BeTrue(), "image registry identity must be created")
				g.Expect(registryFederatedCreated).To(BeTrue(), "image registry federated credentials must be created")
				g.Expect(out.ImageRegistry.ClientID).ToNot(BeEmpty())
			} else {
				g.Expect(registryIdentityCreated).To(BeFalse(), "image registry identity must be skipped when ImageRegistry is disabled")
				g.Expect(registryFederatedCreated).To(BeFalse(), "image registry federated credentials must be skipped when ImageRegistry is disabled")
				g.Expect(out.ImageRegistry).To(Equal(hyperv1.WorkloadIdentity{}), "no registry client ID should be recorded when disabled")
			}
		})
	}
}
