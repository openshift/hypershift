package azure

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	azureauth "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
)

// mockRoleAssignmentClient implements roleAssignmentClient for testing.
type mockRoleAssignmentClient struct {
	// getFunc is called when Get is invoked.
	getFunc func(ctx context.Context, scope, name string, options *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error)
	// deleteFunc is called when Delete is invoked.
	deleteFunc func(ctx context.Context, scope, name string, options *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error)
	// createFunc is called when Create is invoked.
	createFunc func(ctx context.Context, scope, name string, params azureauth.RoleAssignmentCreateParameters, options *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error)
	// listItems are returned by the pager from NewListForScopePager.
	listItems []*azureauth.RoleAssignment
	// listErr if set causes the pager to return this error.
	listErr error
}

func (m *mockRoleAssignmentClient) Get(ctx context.Context, scope, name string, options *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error) {
	return m.getFunc(ctx, scope, name, options)
}

func (m *mockRoleAssignmentClient) Delete(ctx context.Context, scope, name string, options *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
	return m.deleteFunc(ctx, scope, name, options)
}

func (m *mockRoleAssignmentClient) Create(ctx context.Context, scope, name string, params azureauth.RoleAssignmentCreateParameters, options *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error) {
	return m.createFunc(ctx, scope, name, params, options)
}

func (m *mockRoleAssignmentClient) NewListForScopePager(_ string, _ *azureauth.RoleAssignmentsClientListForScopeOptions) *runtime.Pager[azureauth.RoleAssignmentsClientListForScopeResponse] {
	items := m.listItems
	listErr := m.listErr
	return runtime.NewPager(runtime.PagingHandler[azureauth.RoleAssignmentsClientListForScopeResponse]{
		More: func(_ azureauth.RoleAssignmentsClientListForScopeResponse) bool {
			return false
		},
		Fetcher: func(_ context.Context, _ *azureauth.RoleAssignmentsClientListForScopeResponse) (azureauth.RoleAssignmentsClientListForScopeResponse, error) {
			if listErr != nil {
				return azureauth.RoleAssignmentsClientListForScopeResponse{}, listErr
			}
			return azureauth.RoleAssignmentsClientListForScopeResponse{
				RoleAssignmentListResult: azureauth.RoleAssignmentListResult{
					Value: items,
				},
			}, nil
		},
	})
}

func notFoundError() error {
	return &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "RoleAssignmentNotFound",
	}
}

func forbiddenError() error {
	return &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "AuthorizationFailed",
	}
}

func conflictError() error {
	return &azcore.ResponseError{
		StatusCode: http.StatusConflict,
		ErrorCode:  "RoleAssignmentExists",
	}
}

func internalServerError() error {
	return &azcore.ResponseError{
		StatusCode: http.StatusInternalServerError,
		ErrorCode:  "InternalServerError",
	}
}

func TestAssignRole(t *testing.T) {
	const (
		subscriptionID = "test-sub-id"
		infraID        = "test-infra"
		component      = "ingress"
		currentPrinc   = "new-principal-id"
		stalePrinc     = "old-principal-id"
		role           = "0336e1d3-7a87-462b-b6db-342b63f7802c"
		scope          = "/subscriptions/test-sub-id/resourceGroups/test-rg"
	)

	roleDefID := "/subscriptions/" + subscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/" + role
	roleAssignmentName := util.GenerateRoleAssignmentName(infraID, component, scope)

	tests := map[string]struct {
		listItems    []*azureauth.RoleAssignment
		listErr      error
		getResponse  *azureauth.RoleAssignmentsClientGetResponse
		getErr       error
		deleteErr    error
		createErr    error
		expectCreate bool
		expectDelete bool
		expectErr    bool
	}{
		// --- LIST behaviors ---
		"When LIST finds matching assignment it should skip creation": {
			listItems: []*azureauth.RoleAssignment{
				{
					Properties: &azureauth.RoleAssignmentProperties{
						PrincipalID:      ptr.To(currentPrinc),
						RoleDefinitionID: ptr.To(roleDefID),
						Scope:            ptr.To(scope),
					},
				},
			},
			getErr:       notFoundError(),
			expectCreate: false,
			expectDelete: false,
		},
		"When LIST returns items with nil properties it should skip them and fall through to GET": {
			listItems: []*azureauth.RoleAssignment{
				{Properties: nil},
				{Properties: &azureauth.RoleAssignmentProperties{
					PrincipalID:      nil,
					RoleDefinitionID: ptr.To(roleDefID),
					Scope:            ptr.To(scope),
				}},
			},
			getErr:       notFoundError(),
			expectCreate: true,
			expectDelete: false,
		},
		"When LIST page returns error it should return error": {
			listErr:   internalServerError(),
			expectErr: true,
		},

		// --- GET behaviors ---
		"When GET finds assignment with matching principal and role it should skip creation": {
			getResponse: &azureauth.RoleAssignmentsClientGetResponse{
				RoleAssignment: azureauth.RoleAssignment{
					Properties: &azureauth.RoleAssignmentProperties{
						PrincipalID:      ptr.To(currentPrinc),
						RoleDefinitionID: ptr.To(roleDefID),
					},
				},
			},
			expectCreate: false,
			expectDelete: false,
		},
		"When GET finds assignment with different principal it should delete stale and create new": {
			getResponse: &azureauth.RoleAssignmentsClientGetResponse{
				RoleAssignment: azureauth.RoleAssignment{
					Properties: &azureauth.RoleAssignmentProperties{
						PrincipalID: ptr.To(stalePrinc),
					},
				},
			},
			expectCreate: true,
			expectDelete: true,
		},
		"When GET finds assignment with nil PrincipalID it should delete stale and create new": {
			getResponse: &azureauth.RoleAssignmentsClientGetResponse{
				RoleAssignment: azureauth.RoleAssignment{
					Properties: &azureauth.RoleAssignmentProperties{
						PrincipalID: nil,
					},
				},
			},
			expectCreate: true,
			expectDelete: true,
		},
		"When GET finds assignment with nil Properties it should delete stale and create new": {
			getResponse: &azureauth.RoleAssignmentsClientGetResponse{
				RoleAssignment: azureauth.RoleAssignment{
					Properties: nil,
				},
			},
			expectCreate: true,
			expectDelete: true,
		},
		"When GET finds stale assignment but delete fails it should return error": {
			getResponse: &azureauth.RoleAssignmentsClientGetResponse{
				RoleAssignment: azureauth.RoleAssignment{
					Properties: &azureauth.RoleAssignmentProperties{
						PrincipalID: ptr.To(stalePrinc),
					},
				},
			},
			deleteErr:    forbiddenError(),
			expectDelete: true,
			expectCreate: false,
			expectErr:    true,
		},
		"When GET returns 404 it should create new assignment": {
			getErr:       notFoundError(),
			expectCreate: true,
			expectDelete: false,
		},
		"When GET returns 403 it should fall through to create": {
			getErr:       forbiddenError(),
			expectCreate: true,
			expectDelete: false,
		},
		"When GET returns unexpected API error it should return error": {
			getErr:    internalServerError(),
			expectErr: true,
		},
		"When GET returns non-API error it should return error": {
			getErr:    fmt.Errorf("network timeout"),
			expectErr: true,
		},

		// --- Create behaviors ---
		"When create returns 409 conflict it should succeed": {
			getErr:       notFoundError(),
			createErr:    conflictError(),
			expectCreate: true,
			expectDelete: false,
			expectErr:    false,
		},
		"When create returns unexpected error it should return error": {
			getErr:       notFoundError(),
			createErr:    internalServerError(),
			expectCreate: true,
			expectDelete: false,
			expectErr:    true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			var (
				created     bool
				deleted     bool
				createdName string
				deletedName string
			)

			mock := &mockRoleAssignmentClient{
				listItems: tc.listItems,
				listErr:   tc.listErr,
				getFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error) {
					if tc.getResponse != nil {
						return *tc.getResponse, nil
					}
					return azureauth.RoleAssignmentsClientGetResponse{}, tc.getErr
				},
				deleteFunc: func(_ context.Context, _, name string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
					deleted = true
					deletedName = name
					if tc.deleteErr != nil {
						return azureauth.RoleAssignmentsClientDeleteResponse{}, tc.deleteErr
					}
					return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
				},
				createFunc: func(_ context.Context, _, name string, _ azureauth.RoleAssignmentCreateParameters, _ *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error) {
					created = true
					createdName = name
					if tc.createErr != nil {
						return azureauth.RoleAssignmentsClientCreateResponse{}, tc.createErr
					}
					return azureauth.RoleAssignmentsClientCreateResponse{}, nil
				},
			}

			mgr := &RBACManager{subscriptionID: subscriptionID}
			err := mgr.assignRole(t.Context(), mock, infraID, component, currentPrinc, role, scope)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(created).To(Equal(tc.expectCreate), "create expectation mismatch")
			g.Expect(deleted).To(Equal(tc.expectDelete), "delete expectation mismatch")

			if tc.expectCreate {
				g.Expect(createdName).To(Equal(roleAssignmentName), "should use deterministic role assignment name")
			}
			if tc.expectDelete {
				g.Expect(deletedName).To(Equal(roleAssignmentName), "should delete the stale role assignment by deterministic name")
			}
		})
	}
}

func TestAssignRoleCreateParameters(t *testing.T) {
	t.Run("When creating a new assignment it should pass correct principal ID, role definition, and scope", func(t *testing.T) {
		g := NewWithT(t)

		const (
			subscriptionID = "test-sub-id"
			infraID        = "test-infra"
			component      = "ingress"
			assigneeID     = "principal-123"
			role           = "0336e1d3-7a87-462b-b6db-342b63f7802c"
			scope          = "/subscriptions/test-sub-id/resourceGroups/test-rg"
		)

		expectedRoleDefID := "/subscriptions/" + subscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/" + role
		expectedName := util.GenerateRoleAssignmentName(infraID, component, scope)

		var capturedParams azureauth.RoleAssignmentCreateParameters
		var capturedScope, capturedName string

		mock := &mockRoleAssignmentClient{
			getFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientGetOptions) (azureauth.RoleAssignmentsClientGetResponse, error) {
				return azureauth.RoleAssignmentsClientGetResponse{}, notFoundError()
			},
			deleteFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
				return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
			},
			createFunc: func(_ context.Context, s, n string, params azureauth.RoleAssignmentCreateParameters, _ *azureauth.RoleAssignmentsClientCreateOptions) (azureauth.RoleAssignmentsClientCreateResponse, error) {
				capturedScope = s
				capturedName = n
				capturedParams = params
				return azureauth.RoleAssignmentsClientCreateResponse{}, nil
			},
		}

		mgr := &RBACManager{subscriptionID: subscriptionID}
		err := mgr.assignRole(t.Context(), mock, infraID, component, assigneeID, role, scope)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(capturedScope).To(Equal(scope))
		g.Expect(capturedName).To(Equal(expectedName))
		g.Expect(capturedParams.Properties).ToNot(BeNil())
		g.Expect(*capturedParams.Properties.PrincipalID).To(Equal(assigneeID))
		g.Expect(*capturedParams.Properties.RoleDefinitionID).To(Equal(expectedRoleDefID))
		g.Expect(*capturedParams.Properties.Scope).To(Equal(scope))
	})
}

func TestDeleteRoleAssignmentByName(t *testing.T) {
	tests := map[string]struct {
		deleteErr   error
		expectError bool
	}{
		"When assignment exists it should delete successfully": {
			deleteErr:   nil,
			expectError: false,
		},
		"When assignment does not exist it should skip gracefully": {
			deleteErr:   notFoundError(),
			expectError: false,
		},
		"When delete fails with unexpected error it should return error": {
			deleteErr:   forbiddenError(),
			expectError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			mock := &mockRoleAssignmentClient{
				deleteFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
					if tc.deleteErr != nil {
						return azureauth.RoleAssignmentsClientDeleteResponse{}, tc.deleteErr
					}
					return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
				},
			}

			mgr := &RBACManager{subscriptionID: "test-sub"}
			err := mgr.deleteRoleAssignmentByName(t.Context(), discardLogger(), mock, "/subscriptions/test-sub/resourceGroups/rg", "test-name", "test-component")

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

// deleteCall records a single delete invocation for verification.
type deleteCall struct {
	scope string
	name  string
}

func TestCleanupRoleAssignments(t *testing.T) {
	const (
		subscriptionID = "test-sub-id"
		infraID        = "test-infra"
		managedRG      = "test-rg"
		nsgRG          = "test-nsg-rg"
		vnetRG         = "test-vnet-rg"
		dnsZoneRG      = "os4-common"
	)

	managedRGScope := "/subscriptions/" + subscriptionID + "/resourceGroups/" + managedRG
	dnsZoneScope := "/subscriptions/" + subscriptionID + "/resourceGroups/" + dnsZoneRG
	vnetRGScope := "/subscriptions/" + subscriptionID + "/resourceGroups/" + vnetRG

	t.Run("When all assignments exist it should delete all control plane and data plane assignments", func(t *testing.T) {
		g := NewWithT(t)

		var mu sync.Mutex
		var calls []deleteCall

		mock := &mockRoleAssignmentClient{
			deleteFunc: func(_ context.Context, scope, name string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
				mu.Lock()
				calls = append(calls, deleteCall{scope: scope, name: name})
				mu.Unlock()
				return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
			},
		}

		mgr := &RBACManager{subscriptionID: subscriptionID}
		err := mgr.cleanupRoleAssignments(t.Context(), discardLogger(), mock, infraID, managedRG, nsgRG, vnetRG, dnsZoneRG, false)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify the ingress component's DNS zone scope is cleaned up (the exact bug scenario).
		ingressDNSName := util.GenerateRoleAssignmentName(infraID, config.Ingress, dnsZoneScope)
		g.Expect(calls).To(ContainElement(deleteCall{scope: dnsZoneScope, name: ingressDNSName}),
			"should clean up ingress role assignment on DNS zone scope")

		// Verify the ingress component's vnet scope is cleaned up.
		ingressVNetName := util.GenerateRoleAssignmentName(infraID, config.Ingress, vnetRGScope)
		g.Expect(calls).To(ContainElement(deleteCall{scope: vnetRGScope, name: ingressVNetName}),
			"should clean up ingress role assignment on vnet scope")

		// Verify data plane WI-suffixed components are cleaned up on managed RG.
		for _, dp := range []string{config.CIRO + "WI", config.AzureDisk + "WI", config.AzureFile + "WI"} {
			dpName := util.GenerateRoleAssignmentName(infraID, dp, managedRGScope)
			g.Expect(calls).To(ContainElement(deleteCall{scope: managedRGScope, name: dpName}),
				"should clean up data plane component %s", dp)
		}

		// All 8 control plane components + 3 data plane components should produce delete calls.
		// Exact count depends on GetServicePrincipalScopes; verify at least the minimum.
		g.Expect(len(calls)).To(BeNumerically(">=", 11),
			"should delete assignments for all components across their scopes")
	})

	t.Run("When some assignments are not found it should continue and succeed", func(t *testing.T) {
		g := NewWithT(t)

		deleteCount := 0
		mock := &mockRoleAssignmentClient{
			deleteFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
				deleteCount++
				// Every other call returns 404
				if deleteCount%2 == 0 {
					return azureauth.RoleAssignmentsClientDeleteResponse{}, notFoundError()
				}
				return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
			},
		}

		mgr := &RBACManager{subscriptionID: subscriptionID}
		err := mgr.cleanupRoleAssignments(t.Context(), discardLogger(), mock, infraID, managedRG, nsgRG, vnetRG, dnsZoneRG, false)
		g.Expect(err).ToNot(HaveOccurred(), "404s should be treated as success")
		g.Expect(deleteCount).To(BeNumerically(">=", 11), "should attempt all components even when some return 404")
	})

	t.Run("When some deletes fail with non-404 errors it should continue and return aggregate error", func(t *testing.T) {
		g := NewWithT(t)

		deleteCount := 0
		failCount := 0
		mock := &mockRoleAssignmentClient{
			deleteFunc: func(_ context.Context, _, _ string, _ *azureauth.RoleAssignmentsClientDeleteOptions) (azureauth.RoleAssignmentsClientDeleteResponse, error) {
				deleteCount++
				// Fail every 5th call with a non-404 error
				if deleteCount%5 == 0 {
					failCount++
					return azureauth.RoleAssignmentsClientDeleteResponse{}, forbiddenError()
				}
				return azureauth.RoleAssignmentsClientDeleteResponse{}, nil
			},
		}

		mgr := &RBACManager{subscriptionID: subscriptionID}
		err := mgr.cleanupRoleAssignments(t.Context(), discardLogger(), mock, infraID, managedRG, nsgRG, vnetRG, dnsZoneRG, false)
		g.Expect(err).To(HaveOccurred(), "should return error when some deletes fail")
		g.Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("failed to delete %d role assignments", failCount)))
		g.Expect(deleteCount).To(BeNumerically(">=", 11),
			"should attempt all components even when some fail")
	})
}

func discardLogger() logr.Logger {
	return logr.Discard()
}
