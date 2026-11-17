package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/config"

	azureauth "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func TestHandleIdentitiesAndRBAC(t *testing.T) {
	const clientID = "12345678-1234-1234-1234-123456789012"
	const componentIdentities = `{
		"cloudProvider":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"nodePoolManagement":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"controlPlaneOperator":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"imageRegistry":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"ingress":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"network":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"disk":{"clientID":"12345678-1234-1234-1234-123456789012"},
		"file":{"clientID":"12345678-1234-1234-1234-123456789012"}
	}`
	workloadIdentities := strings.TrimSuffix(componentIdentities, "}") + `,"karpenterClientID":"12345678-1234-1234-1234-123456789012","kmsClientID":"23456789-1234-1234-1234-123456789012"}`
	type grant struct {
		principalID string
		role        string
		scope       string
	}
	const clusterScope = "/subscriptions/test-subscription/resourceGroups/cluster-rg"
	const vnetScope = "/subscriptions/test-subscription/resourceGroups/vnet-rg"
	const rolePrefix = "/subscriptions/test-subscription/providers/Microsoft.Authorization/roleDefinitions/"
	objectIDs := map[string]string{
		clientID:                               "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		"23456789-1234-1234-1234-123456789012": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"34567890-1234-1234-1234-123456789012": "cccccccc-cccc-cccc-cccc-cccccccccccc",
	}
	tests := map[string]struct {
		managed                   string // JSON/YAML contents of --managed-identities-file, not a file path.
		workload                  string // JSON contents of --workload-identities-file, not a file path.
		karpenterClientID         string // Value of the explicit --karpenter-client-id option.
		wantKarpenterClientID     string // Expected Karpenter client ID after applying flag-over-file precedence.
		wantKMSClientID           string // Expected KMS client ID in infrastructure output.
		wantCloudProviderClientID string // Expected cloud-provider client ID preserved from the identity file.
		wantErr                   string // Expected error substring. Empty means success.
		assignRoles               bool   // Enable role assignment with fake clients and check Karpenter grants.
	}{
		// Identity parsing and selection without role assignment.
		"When no identities are supplied, it should leave optional client IDs empty": {
			wantKarpenterClientID: "",
			wantKMSClientID:       "",
		},
		"When managed identities are supplied, it should load the cloud-provider client ID": {
			managed:                   componentIdentities,
			wantKarpenterClientID:     "",
			wantKMSClientID:           "",
			wantCloudProviderClientID: clientID,
		},
		"When managed identities use YAML, it should preserve component IDs and the explicit Karpenter client ID": {
			managed:                   "cloudProvider:\n  clientID: " + clientID + "\n",
			karpenterClientID:         clientID,
			wantKarpenterClientID:     clientID,
			wantKMSClientID:           "",
			wantCloudProviderClientID: clientID,
		},
		"When managed identities contain unsupported fields, it should ignore them": {
			managed:                   workloadIdentities,
			wantKarpenterClientID:     "",
			wantKMSClientID:           "",
			wantCloudProviderClientID: clientID,
		},
		"When workload identities omit optional client IDs and no override is supplied, it should leave those IDs empty": {
			workload:                  componentIdentities,
			wantKarpenterClientID:     "",
			wantKMSClientID:           "",
			wantCloudProviderClientID: clientID,
		},
		"When workload identities omit an optional client ID, it should use the explicit value": {
			workload:                  componentIdentities,
			karpenterClientID:         clientID,
			wantKarpenterClientID:     clientID,
			wantKMSClientID:           "",
			wantCloudProviderClientID: clientID,
		},
		"When workload-file and explicit client IDs match, it should accept the matching value": {
			workload:                  workloadIdentities,
			karpenterClientID:         clientID,
			wantKarpenterClientID:     clientID,
			wantKMSClientID:           "23456789-1234-1234-1234-123456789012",
			wantCloudProviderClientID: clientID,
		},

		// Invalid identity files.
		"When managed identities contain invalid JSON, it should return an error": {
			managed: `{"`,
			wantErr: "failed to unmarshal --managed-identities-file",
		},
		"When workload identities contain invalid JSON, it should return an error": {
			workload: `{"`,
			wantErr:  "failed to unmarshal --workload-identities-file",
		},

		// Role assignment to the selected identity.
		"When role assignment is enabled with managed identities, it should grant roles to the explicit identity": {
			managed:                   strings.ReplaceAll(componentIdentities, clientID, "23456789-1234-1234-1234-123456789012"),
			karpenterClientID:         clientID,
			wantKarpenterClientID:     clientID,
			wantKMSClientID:           "",
			wantCloudProviderClientID: "23456789-1234-1234-1234-123456789012",
			assignRoles:               true,
		},
		"When role assignment is enabled with workload identities, it should grant roles to the identity from the file": {
			workload:                  workloadIdentities,
			wantKarpenterClientID:     clientID,
			wantKMSClientID:           "23456789-1234-1234-1234-123456789012",
			wantCloudProviderClientID: clientID,
			assignRoles:               true,
		},
		"When role assignment is enabled and file and explicit client IDs differ, it should grant roles to the explicit identity": {
			workload:                  workloadIdentities,
			karpenterClientID:         "34567890-1234-1234-1234-123456789012",
			wantKarpenterClientID:     "34567890-1234-1234-1234-123456789012",
			wantKMSClientID:           "23456789-1234-1234-1234-123456789012",
			wantCloudProviderClientID: clientID,
			assignRoles:               true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			opts := &CreateInfraOptions{
				KarpenterClientID:           tc.karpenterClientID,
				AssignServicePrincipalRoles: tc.assignRoles,
				InfraID:                     "test-infra",
				DNSZoneRG:                   "dns-rg",
			}
			if tc.managed != "" {
				opts.ManagedIdentitiesFile = filepath.Join(t.TempDir(), "managed-identities.json")
				g.Expect(os.WriteFile(opts.ManagedIdentitiesFile, []byte(tc.managed), 0600)).To(Succeed())
			}
			if tc.workload != "" {
				opts.WorkloadIdentitiesFile = filepath.Join(t.TempDir(), "workload-identities.json")
				g.Expect(os.WriteFile(opts.WorkloadIdentitiesFile, []byte(tc.workload), 0600)).To(Succeed())
			}
			var rbacMgr *RBACManager
			var grants []grant
			if tc.assignRoles {
				rbacMgr = NewRBACManager("test-subscription", testTokenCredential{})
				rbacMgr.graphClient = httpClientFunc(func(req *http.Request) (*http.Response, error) {
					id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Query().Get("$filter"), "appId eq '"), "'")
					objectID, found := objectIDs[id]
					g.Expect(found).To(BeTrue(), "unexpected client ID: %s", id)
					return graphResponse(req, http.StatusOK, fmt.Sprintf(`{"value":[{"id":%q}]}`, objectID)), nil
				})
				rbacMgr.roleClient = &mockRoleAssignmentClient{
					getFunc: func(context.Context, string, string, *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error) {
						return azureauth.RoleAssignmentsClientGetResponse{}, notFoundError()
					},
					createFunc: func(_ context.Context, scope, name string, params azureauth.RoleAssignmentCreateParameters, _ *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error) {
						for _, component := range []string{config.KarpenterVM, config.KarpenterNetwork, config.KarpenterMI} {
							if name == util.GenerateRoleAssignmentName(opts.InfraID, component, scope) {
								grants = append(grants, grant{*params.Properties.PrincipalID, *params.Properties.RoleDefinitionID, scope})
							}
						}
						return azureauth.RoleAssignmentsClientCreateResponse{}, nil
					},
				}
			}
			result := &CreateInfraOutput{}
			err := opts.handleIdentitiesAndRBAC(t.Context(), rbacMgr, result, "cluster-rg", "nsg-rg", "vnet-rg")
			if tc.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.KarpenterClientID).To(Equal(tc.wantKarpenterClientID))
			g.Expect(result.KMSClientID).To(Equal(tc.wantKMSClientID))
			if tc.assignRoles {
				g.Expect(grants).To(ConsistOf(
					grant{objectIDs[tc.wantKarpenterClientID], rolePrefix + config.VirtualMachineContributorRoleDefinitionID, clusterScope},
					grant{objectIDs[tc.wantKarpenterClientID], rolePrefix + config.NetworkContributorRoleDefinitionID, clusterScope},
					grant{objectIDs[tc.wantKarpenterClientID], rolePrefix + config.ManagedIdentityOperatorRoleDefinitionID, clusterScope},
					grant{objectIDs[tc.wantKarpenterClientID], rolePrefix + config.NetworkContributorRoleDefinitionID, vnetScope},
				))
			}
			if tc.workload != "" {
				g.Expect(result.WorkloadIdentities).NotTo(BeNil())
				g.Expect(result.ControlPlaneMIs).To(BeNil())
				g.Expect(string(result.WorkloadIdentities.CloudProvider.ClientID)).To(Equal(tc.wantCloudProviderClientID))
			}
			if tc.managed != "" {
				g.Expect(result.ControlPlaneMIs).NotTo(BeNil())
				g.Expect(result.WorkloadIdentities).To(BeNil())
				g.Expect(string(result.ControlPlaneMIs.ControlPlane.CloudProvider.ClientID)).To(Equal(tc.wantCloudProviderClientID))
			}

			raw, err := json.Marshal(result)
			g.Expect(err).NotTo(HaveOccurred())
			roundTrip := &CreateInfraOutput{}
			g.Expect(json.Unmarshal(raw, roundTrip)).To(Succeed())
			g.Expect(roundTrip).To(Equal(result))
		})
	}
}

func TestNewCreateCommand(t *testing.T) {
	t.Run("When an explicit Karpenter client ID is supplied, it should accept the flag", func(t *testing.T) {
		g := NewWithT(t)
		cmd := NewCreateCommand()
		g.Expect(cmd.ParseFlags([]string{"--karpenter-client-id=12345678-1234-1234-1234-123456789012"})).To(Succeed())
		g.Expect(cmd.Flags().Lookup("karpenter-client-id").Value.String()).To(Equal("12345678-1234-1234-1234-123456789012"))
	})
}

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		errorContains string
		description   string
	}{
		"When base domain is provided it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain: "example.com",
			},
			expectedError: false,
			description:   "Should pass when base domain is provided",
		},
		"When base domain is missing it should return an error": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			errorContains: "--base-domain is required",
			description:   "Should require base domain",
		},
		"When assign-identity-roles is set without dns-zone-rg-name it should return an error": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: true,
				DNSZoneRG:                   "",
			},
			expectedError: true,
			errorContains: "--dns-zone-rg-name is required when --assign-identity-roles or --assign-custom-hcp-roles is set",
			description:   "Should require dns-zone-rg-name when assign-identity-roles is set",
		},
		"When assign-custom-hcp-roles is set without dns-zone-rg-name it should return an error": {
			opts: CreateInfraOptions{
				BaseDomain:           "example.com",
				AssignCustomHCPRoles: true,
				DNSZoneRG:            "",
			},
			expectedError: true,
			errorContains: "--dns-zone-rg-name is required when --assign-identity-roles or --assign-custom-hcp-roles is set",
			description:   "Should require dns-zone-rg-name when assign-custom-hcp-roles is set",
		},
		"When assign-identity-roles is set with dns-zone-rg-name it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: true,
				DNSZoneRG:                   "my-dns-rg",
			},
			expectedError: false,
			description:   "Should pass when both assign-identity-roles and dns-zone-rg-name are set",
		},
		"When assign-identity-roles is not set and dns-zone-rg-name is empty it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: false,
				DNSZoneRG:                   "",
			},
			expectedError: false,
			description:   "Should pass when assign-identity-roles is not set regardless of dns-zone-rg-name",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.Validate()

			if test.expectedError {
				g.Expect(err).To(HaveOccurred(), test.description)
				g.Expect(err.Error()).To(ContainSubstring(test.errorContains), test.description)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), test.description)
			}
		})
	}
}

func TestValidateDeploymentModelFlags(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		description   string
	}{
		"When both ARO HCP managed and data plane identities are provided it should pass validation": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
			},
			expectedError: false,
			description:   "Should allow both ARO HCP identity files together",
		},
		"When workload identities file is provided alone it should pass validation": {
			opts: CreateInfraOptions{
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: false,
			description:   "Should allow workload identities file alone",
		},
		"When no identity configuration is provided it should return an error": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			description:   "Should reject empty configuration without any identity settings",
		},
		"When ARO HCP managed identities are mixed with workload identities it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:  "mi.json",
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP managed identities with self-managed workload identities",
		},
		"When ARO HCP data plane identities are mixed with workload identities it should return an error": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP data plane identities with self-managed workload identities",
		},
		"When all deployment models are mixed together it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing all deployment models together",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateDeploymentModelFlags()

			if test.expectedError {
				g.Expect(err).To(HaveOccurred(), test.description)
				// Verify the error mentions either mutual exclusion or missing configuration
				g.Expect(err.Error()).To(SatisfyAny(
					ContainSubstring("mutually exclusive"),
					ContainSubstring("--workload-identities-file is required"),
				), "Error should mention validation failure: %s", test.description)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), test.description)
			}
		})
	}
}
