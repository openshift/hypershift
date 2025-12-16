# OCPBUGS-33097: Remove OVNSbDb Service Validation

## Problem Summary

When creating a HostedCluster with network type 'Other', the CLI correctly generates a HostedCluster without an OVNSbDb service in the services section. However, the HyperShift operator validation still requires OVNSbDb service, causing the HostedCluster to be marked as InvalidConfiguration and preventing it from progressing.

The OVNSbDb service is no longer needed in OpenShift 4.14+ and should be removed from:
1. API validation rules
2. Any documentation or comments referring to it as required

## Context

From the codebase analysis:
- **API Definition**: `/api/hypershift/v1beta1/hostedcluster_types.go`
  - Line 585: Comment says "Max is 6 to account for OIDC;OVNSbDb for backward compatibility though they are no-op"
  - Line 822: Comment says "OVNSbDb;OIDC are no-op and kept for backward compatibility"
  - Line 825: OVNSbDb is still in the kubebuilder enum validation
  - Lines 897-899: OVNSbDb is marked as deprecated with comment "This service is no longer used by OVNKubernetes CNI for >= 4.14"
  - Line 447: Validation rule requires at least 4 services (or 3 for IBMCloud) but doesn't specifically validate for OVNSbDb

The issue is NOT that OVNSbDb is required by validation (it's actually already deprecated), but that the comments and maximum items count (6) suggest it might be. The real issue appears to be that users may have OVNSbDb in their services list from older configs, and we need to ensure the system handles it gracefully.

After reviewing the code more carefully:
- Line 447 XValidation: `self.platform.type == 'IBMCloud' ? size(self.services) >= 3 : size(self.services) >= 4`
- This only checks the SIZE of services, not specific service types
- The commented-out validation on line 590 is what would check for specific required services, but it's disabled (note the `-` prefix)

## Root Cause

After thorough analysis, the root cause is found in the API validation at line 447 of `api/hypershift/v1beta1/hostedcluster_types.go`:

```go
// +kubebuilder:validation:XValidation:rule="self.platform.type == 'IBMCloud' ? size(self.services) >= 3 : size(self.services) >= 4",message="spec.services in body should have at least 4 items or 3 for IBMCloud"
```

This CEL validation requires:
- IBMCloud platform: at least 3 services
- All other platforms: at least 4 services

The issue is that this validation doesn't account for network type 'Other'. When using network type 'Other':
- Ignition service may not be needed (as it's for node ignition which may be handled differently)
- OVNSbDb is NOT needed (deprecated for >= 4.14)

So a valid HostedCluster with network type 'Other' might only have 3 services:
1. APIServer
2. OAuthServer
3. Konnectivity

But the validation requires 4, causing it to fail.

## Solution Plan

### Phase 1: Update Size Validation for Services
**Goal**: Modify the API validation to allow 3 services for all platforms (not just IBMCloud)

The fix is to update line 447 in `api/hypershift/v1beta1/hostedcluster_types.go` from:
```go
// +kubebuilder:validation:XValidation:rule="self.platform.type == 'IBMCloud' ? size(self.services) >= 3 : size(self.services) >= 4",message="spec.services in body should have at least 4 items or 3 for IBMCloud"
```

To:
```go
// +kubebuilder:validation:XValidation:rule="size(self.services) >= 3",message="spec.services in body should have at least 3 items"
```

This allows all platforms to have a minimum of 3 services (APIServer, OAuthServer, Konnectivity), making Ignition and OVNSbDb truly optional.

### Phase 2: Update Comments and Documentation
**Goal**: Clarify that only 3 services are actually required

1. **Update service requirements comment** (line 583)
   - Change from: "This requires APIServer;OAuthServer;Konnectivity;Ignition."
   - Change to: "This requires APIServer;OAuthServer;Konnectivity. Ignition is required for most platforms but may be optional depending on the platform and network configuration."

2. **Keep MaxItems=6** (line 587) - for backward compatibility with deprecated OIDC and OVNSbDb

3. **Update ServicePublishingStrategyMapping comment** (line 817)
   - Change from: "This includes APIServer;OAuthServer;Konnectivity;Ignition."
   - Change to: "This includes APIServer;OAuthServer;Konnectivity. Ignition is required for most platforms."

### Phase 3: Testing
**Goal**: Ensure the fix works for network type 'Other'

1. Review test files that reference OVNSbDb:
   - `test/e2e/create_cluster_test.go:879` (commented out)
   - `control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller_test.go:1200`
2. Update any tests that expect OVNSbDb to be required
3. Add or update tests for network type 'Other' without OVNSbDb

### Phase 4: Regenerate CRDs and Documentation
**Goal**: Ensure generated files reflect the changes

1. Run `make api` to regenerate CRDs
2. Run `make lint-fix` to fix any linting issues
3. Run `make verify` to ensure all verifications pass
4. Update any documentation in `docs/` if needed

## Implementation Steps

1. **Update API validation** - Change size requirement from 4 to 3 services (line 447)
2. **Update comments** - Clarify service requirements in comments (lines 583, 817)
3. **Regenerate APIs** - Run `make api` to regenerate CRDs
4. **Verify changes** - Run `make lint-fix` and `make verify`
5. **Review tests** - Check if any tests need updating

## Files to Modify

1. `api/hypershift/v1beta1/hostedcluster_types.go` - Update validation rule and comments
2. Generated CRD files (via `make api`):
   - `cmd/install/assets/hypershift-operator/zz_generated.crd-manifests/*.yaml`
   - `api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests/**/*.yaml`

## Acceptance Criteria

- [ ] Creating a HostedCluster with 3 services (APIServer, OAuthServer, Konnectivity) succeeds
- [ ] No validation error about minimum 4 services required
- [ ] API validation rule requires minimum 3 services instead of 4
- [ ] API comments accurately reflect that only 3 services are required
- [ ] All tests pass
- [ ] `make verify` passes
- [ ] CRDs are regenerated and updated

## Risks and Considerations

1. **Backward Compatibility**: Changing from 4 to 3 minimum services is backward compatible - existing clusters with 4+ services will continue to work
2. **Ignition Service**: Making Ignition optional could break some platforms if they require it. Need to verify which platforms actually need Ignition
3. **IBMCloud Platform**: This change means IBMCloud no longer has special treatment (was 3, now all platforms are 3)

## Alternative Approaches Considered

1. **Add network type to validation**: Could make the validation network-type-aware, but this is more complex
2. **Keep 4 services for non-IBMCloud**: Would not fix the issue for network type 'Other'
3. **Remove size validation entirely**: Too permissive, could allow invalid configurations
