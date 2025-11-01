# Implementation Specification: OCPBUGS-20470

## Problem Statement

Currently, HyperShift hosted clusters allow setting `tokenConfig.accessTokenInactivityTimeout` to values less than 300 seconds, which doesn't align with the validation in management clusters. In management clusters, attempting to set this value below 300s results in an error:

```
spec.tokenConfig.accessTokenInactivityTimeout: Invalid value: v1.Duration{Duration:100000000000}: the minimum acceptable token timeout value is 300 seconds
```

This inconsistency creates confusion and unexpected behavior where:
1. Users can set accessTokenInactivityTimeout < 300s in hosted clusters without error
2. The timeout doesn't actually work as expected (tokens remain valid beyond the configured timeout)
3. There's no validation to prevent this misconfiguration

## Analysis

I've located the relevant code:
- **API Definition**: `api/hypershift/v1beta1/hostedcluster_types.go:1909`
  - The OAuth configuration already has a CEL validation rule for accessTokenInactivityTimeout
  - Current rule: `!has(self.tokenConfig) || !has(self.tokenConfig.accessTokenInactivityTimeout) || duration(self.tokenConfig.accessTokenInactivityTimeout).getSeconds() >= 300`
  - Message: `spec.configuration.oauth.tokenConfig.accessTokenInactivityTimeout minimum acceptable token timeout value is 300 seconds`

**Key Finding**: The validation **already exists** in the API definition at line 1909! This is a CEL (Common Expression Language) validation that should be enforced by Kubernetes at the API level.

## Root Cause

The issue reported in OCPBUGS-20470 suggests that users can set values < 300s without getting an error. This indicates that either:

1. The CEL validation is not being enforced (possibly due to feature gate or API server configuration)
2. The validation was added after the bug was reported
3. There's a way to bypass the validation

Let me verify when this validation was added by checking the git history.

## Proposed Solution

Since the validation already exists in the CRD, the solution depends on what we find:

### Option A: Validation exists but needs to be regenerated in CRDs
If the validation exists in the Go types but not in the generated CRDs:
1. Run `make api` to regenerate CRDs with the validation
2. Add test cases to verify the validation works

### Option B: Validation was recently added and needs backporting
If this is a recent fix:
1. Verify the validation is in all necessary places
2. Add comprehensive test coverage
3. Document the fix

### Option C: Validation exists but is not working
If validation exists but isn't being enforced:
1. Investigate why CEL validation isn't being enforced
2. Consider adding webhook validation as a backup
3. Add test coverage

## Implementation Steps

1. **Verify Current State**
   - Check generated CRD manifests to see if validation is present
   - Verify if the validation is actually enforced

2. **If Validation Needs to be Added/Fixed**
   - Add or update CEL validation in `api/hypershift/v1beta1/hostedcluster_types.go`
   - Run `make api` to regenerate CRDs

3. **Add Test Coverage**
   - Add unit tests in `api/hypershift/v1beta1/hostedcluster_validation_test.go` (create if doesn't exist)
   - Test cases to include:
     - Valid timeout >= 300s
     - Invalid timeout < 300s (should fail)
     - Edge case: exactly 300s (should pass)
     - Edge case: 299s (should fail)
     - No timeout specified (should pass)

4. **Verify the Fix**
   - Run `make lint-fix` to ensure proper formatting
   - Run `make verify` to ensure all generated code is up to date
   - Run `make test` to verify unit tests pass
   - Run `make build` to ensure the code compiles

## Files to Modify

1. **api/hypershift/v1beta1/hostedcluster_types.go** (if validation needs update)
   - Line 1909: Verify/update CEL validation

2. **api/hypershift/v1beta1/hostedcluster_validation_test.go** (create new)
   - Add comprehensive test coverage for OAuth tokenConfig validation

3. **Generated CRD files** (via `make api`)
   - All CRD manifests under `cmd/install/assets/hypershift-operator/zz_generated.crd-manifests/`
   - All CRD manifests under `api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests/`

## Test Plan

### Unit Tests
1. Test valid accessTokenInactivityTimeout values (>= 300s)
2. Test invalid accessTokenInactivityTimeout values (< 300s)
3. Test boundary conditions (299s, 300s, 301s)
4. Test nil/unset accessTokenInactivityTimeout
5. Test nil/unset tokenConfig

### Manual Verification (if needed)
1. Deploy HyperShift with the fix
2. Attempt to create a HostedCluster with accessTokenInactivityTimeout = 100s
3. Verify error is returned with appropriate message
4. Verify that valid timeout values (>= 300s) are accepted

## Expected Behavior After Fix

When a user attempts to set `spec.configuration.oauth.tokenConfig.accessTokenInactivityTimeout` to a value less than 300 seconds:
- The API server should reject the request with validation error
- Error message should match: `spec.configuration.oauth.tokenConfig.accessTokenInactivityTimeout minimum acceptable token timeout value is 300 seconds`
- This behavior should match management cluster behavior for consistency

## Questions/Clarifications Needed

1. Should we investigate why this validation might not be working currently?
2. Do we need to add webhook validation as a backup, or is CEL validation sufficient?
3. Are there any upgrade considerations for existing clusters with invalid values?
