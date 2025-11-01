# Implementation Specification: OCPBUGS-34027

## Problem Statement

When external authentication is configured on a HyperShift Hosted Control Plane (HCP) cluster with CA and client secrets, the ConfigMaps and Secrets are created in the guest cluster's `openshift-config` namespace. However, when the external authentication configuration is deleted from the HostedControlPlane spec, these resources are not cleaned up, leaving orphaned resources in the guest cluster.

### Example Orphaned Resources
```
ConfigMaps: ext-auth-ca-m-entra-id-<hash>
Secrets: ext-auth-client-m-entra-id-<hash>
```

## Root Cause Analysis

The `reconcileAuthOIDC` function in `control-plane-operator/hostedclusterconfigoperator/controllers/resources/resources.go:1235-1303` only handles creation and updates of OIDC-related ConfigMaps and Secrets. It does NOT handle cleanup/deletion when:

1. OIDC providers are removed from `hcp.Spec.Configuration.Authentication.OIDCProviders`
2. Individual OIDCClients are removed from an OIDC provider
3. The CertificateAuthority reference is removed from an OIDC provider

The function uses `CreateOrUpdate` which ensures resources exist, but never deletes resources that should no longer exist.

## Solution Design

### High-Level Approach

Implement a cleanup mechanism that:
1. Tracks which ConfigMaps and Secrets SHOULD exist based on current HCP configuration
2. Lists existing resources in the `openshift-config` namespace that were created by this controller
3. Deletes resources that are no longer referenced in the HCP spec

### Implementation Steps

#### Step 1: Add Resource Tracking
Add a mechanism to track which resources belong to OIDC authentication:
- Use owner references or labels to identify resources managed by this reconciliation
- Track resource names based on the current HCP configuration

#### Step 2: Modify `reconcileAuthOIDC` Function
Update the function at `control-plane-operator/hostedclusterconfigoperator/controllers/resources/resources.go:1235` to:

1. **Build Expected Resource Set**: Create a set of resource names that SHOULD exist based on current config
   - ConfigMap names from `provider.Issuer.CertificateAuthority.Name`
   - Secret names from `oidcClient.ClientSecret.Name`

2. **List Existing Resources**: Query the `openshift-config` namespace for:
   - ConfigMaps with a label indicating they're managed for OIDC auth
   - Secrets with a label indicating they're managed for OIDC auth

3. **Delete Orphaned Resources**: For each resource found that's NOT in the expected set:
   - Delete the ConfigMap or Secret
   - Log the deletion for audit purposes

#### Step 3: Add Labels to Created Resources
Modify the resource creation logic to add labels:
```go
const (
    OIDCProviderManagedLabel = "hypershift.openshift.io/oidc-provider-managed"
)
```

When creating ConfigMaps and Secrets, add this label:
```go
if dest.Labels == nil {
    dest.Labels = map[string]string{}
}
dest.Labels[OIDCProviderManagedLabel] = "true"
```

#### Step 4: Implement Cleanup Logic
Add cleanup logic that:
1. Lists all ConfigMaps in `openshift-config` namespace with the managed label
2. Lists all Secrets in `openshift-config` namespace with the managed label
3. Compares against expected resources
4. Deletes resources not in the expected set

### Code Changes Required

**File:** `control-plane-operator/hostedclusterconfigoperator/controllers/resources/resources.go`

**Function:** `reconcileAuthOIDC` (starting at line 1235)

**Changes:**
1. Add constants for labels
2. Create helper function to build expected resource set
3. Add labels when creating resources
4. Add cleanup logic after resource creation
5. Handle errors appropriately

### Edge Cases to Handle

1. **ARO HCP Day-2 Secrets**: The code already skips copying secrets with annotation `hypershift.openshift.io/hosted-cluster-sourced: "true"`. These should NOT be deleted during cleanup.

2. **Race Conditions**: If an OIDC provider is being added/removed rapidly, ensure we don't delete resources that are about to be recreated.

3. **Error Handling**: If deletion fails (e.g., permission issues), log the error but don't fail the entire reconciliation.

4. **OAuth Disabled**: The cleanup should only run when `!util.HCPOAuthEnabled(hcp)` (same condition as creation logic).

### Testing Strategy

#### Unit Tests
Create unit tests in `control-plane-operator/hostedclusterconfigoperator/controllers/resources/resources_test.go`:

1. **Test: Resources are created when OIDC provider added**
2. **Test: Resources are deleted when OIDC provider removed**
3. **Test: Resources are deleted when CertificateAuthority reference removed**
4. **Test: Resources are deleted when OIDCClient removed**
5. **Test: ARO HCP day-2 secrets are NOT deleted**
6. **Test: Resources without managed label are NOT deleted**

#### Integration Testing
Manual testing steps:
1. Create HCP cluster with external auth configuration
2. Verify ConfigMaps and Secrets are created in guest cluster
3. Remove external auth configuration from HCP
4. Verify ConfigMaps and Secrets are deleted from guest cluster

### Backward Compatibility

Existing resources created before this change won't have the managed label. To handle this:

**Option 1 (Recommended):** Use naming convention as fallback
- Check if resource name matches the expected name pattern
- If it does and it's not in the expected set, delete it

**Option 2:** Migration approach
- On first reconciliation, add labels to existing matching resources
- Then proceed with normal cleanup logic

**Decision:** Use Option 1 for simplicity and to avoid complex migration logic.

### Rollout Plan

1. Deploy the change to development/staging environments
2. Verify cleanup works correctly with test clusters
3. Monitor for any unexpected deletions
4. Roll out to production environments

### Success Criteria

1. When OIDC provider configuration is removed from HCP spec, corresponding ConfigMaps and Secrets are deleted from guest cluster's `openshift-config` namespace
2. When CertificateAuthority reference is removed, the corresponding ConfigMap is deleted
3. When OIDCClient is removed, the corresponding Secret is deleted
4. No unintended resources are deleted
5. ARO HCP day-2 secrets are preserved
6. Unit tests pass
7. Manual testing confirms the fix works

## Implementation Checklist

- [ ] Add constant for managed resource label
- [ ] Create helper function to build expected resource set
- [ ] Modify ConfigMap creation to add managed label
- [ ] Modify Secret creation to add managed label
- [ ] Implement cleanup logic for ConfigMaps
- [ ] Implement cleanup logic for Secrets
- [ ] Add unit tests
- [ ] Run `make lint-fix`
- [ ] Run `make verify`
- [ ] Manual testing
- [ ] Update documentation if needed
