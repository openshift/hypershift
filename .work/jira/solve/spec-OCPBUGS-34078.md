# Implementation Specification: OCPBUGS-34078

## Problem Summary
Duplicate tag names are currently allowed when creating or updating AWS NodePools through OCM. When duplicate keys are provided (e.g., `{"foo": "bar", "foo": "test"}`), the last value silently overwrites previous ones. This behavior is confusing and should be prevented with a clear validation error.

## Context
From the JIRA issue analysis:
- **Current behavior**: NodePool creation succeeds with duplicate tag keys, keeping only the last value
- **Expected behavior**: Validation should reject duplicate tag keys with error: `"invalid tags, user tag keys must be unique, duplicate key 'foo' found"`
- **Affected component**: AWS NodePool platform-specific validation
- **Entry point**: `nodePool.Spec.Platform.AWS.ResourceTags` field

## Acceptance Criteria
1. Duplicate tag keys in `NodePool.Spec.Platform.AWS.ResourceTags` must be rejected during validation
2. Error message must be clear and specify which key is duplicated: `"invalid tags, user tag keys must be unique, duplicate key 'X' found"`
3. Validation must occur during NodePool creation and updates
4. Unit tests must cover validation logic
5. Must not break existing valid NodePool configurations

## Implementation Plan

### 1. Add Validation Function
**Location**: `hypershift-operator/controllers/nodepool/aws.go`

Add a new validation function `validateResourceTags` that:
- Accepts `[]AWSResourceTag` as input
- Checks for duplicate keys using a map to track seen keys
- Returns an error with the duplicate key name if found
- Is called from `validateAWSPlatformConfig` function

**Rationale**:
- The existing `validateAWSPlatformConfig` at `aws.go:302` already handles AWS-specific validation
- This is the appropriate place to add tag validation logic
- Follows existing pattern of validation functions in the codebase

### 2. Integration Point
**Location**: `hypershift-operator/controllers/nodepool/aws.go:302`

Modify `validateAWSPlatformConfig` function to:
- Call `validateResourceTags(nodePool.Spec.Platform.AWS.ResourceTags)` before existing validation
- Return early if tag validation fails
- Preserve existing validation logic for capacity reservations

**Rationale**:
- This function is already called during NodePool reconciliation at `conditions.go:844`
- It's the central place for AWS platform validation
- No changes needed to reconciliation flow

### 3. Unit Tests
**Location**: `hypershift-operator/controllers/nodepool/aws_test.go`

Add test cases to cover:
- Valid tags with unique keys (should pass)
- Duplicate tag keys (should fail with correct error message)
- Empty tag list (should pass)
- Single tag (should pass)
- Multiple duplicates (should report the first duplicate found)

**Test structure**: Follow existing pattern from `TestValidateAWSPlatformConfig` at `aws_test.go:544`

### 4. Documentation
No additional documentation changes needed as this is internal validation logic.

## Technical Details

### Tag Validation Algorithm
```go
func validateResourceTags(tags []hyperv1.AWSResourceTag) error {
    seenKeys := make(map[string]bool, len(tags))
    for _, tag := range tags {
        if seenKeys[tag.Key] {
            return fmt.Errorf("invalid tags, user tag keys must be unique, duplicate key '%s' found", tag.Key)
        }
        seenKeys[tag.Key] = true
    }
    return nil
}
```

### Integration Example
```go
func (r NodePoolReconciler) validateAWSPlatformConfig(...) error {
    // New validation - check for duplicate tags
    if err := validateResourceTags(nodePool.Spec.Platform.AWS.ResourceTags); err != nil {
        return err
    }

    // Existing validation logic continues...
    if nodePool.Spec.Platform.AWS.Placement != nil && ...
```

## Files to Modify

1. **hypershift-operator/controllers/nodepool/aws.go**
   - Add `validateResourceTags` function (new)
   - Modify `validateAWSPlatformConfig` to call validation (1 line addition)

2. **hypershift-operator/controllers/nodepool/aws_test.go**
   - Add `TestValidateResourceTags` function (new)
   - Optionally extend `TestValidateAWSPlatformConfig` with duplicate tag cases

## Edge Cases Considered

1. **Empty tag list**: Valid, returns no error
2. **Single tag**: Valid, returns no error
3. **Case sensitivity**: AWS tag keys are case-sensitive, so "Foo" and "foo" are different keys (no special handling needed)
4. **Multiple duplicates**: Report the first duplicate encountered
5. **Merged tags**: NodePool tags are merged with HostedCluster tags at `aws.go:176`, but validation should only check NodePool tags for duplicates as:
   - HostedCluster tags are validated separately
   - The merge happens at runtime, not at validation time
   - Users should fix duplicates at the source (NodePool level)

## Risk Assessment

**Risk Level**: Low

**Reasoning**:
- Small, focused change to existing validation code
- No API changes required
- No impact on existing valid configurations
- Follows established validation patterns
- Easy to test and verify

## Rollback Plan

If issues arise, the validation can be removed by:
1. Removing the `validateResourceTags` call from `validateAWSPlatformConfig`
2. Removing the `validateResourceTags` function
3. Removing associated tests

No data migration or backward compatibility concerns.

## Testing Strategy

### Unit Tests
- Test new `validateResourceTags` function directly
- Test integration with `validateAWSPlatformConfig`

### Manual Testing
Test using NodePool CR with:
```yaml
spec:
  platform:
    aws:
      resourceTags:
        - key: "foo"
          value: "bar"
        - key: "foo"
          value: "test"
```
Expected: NodePool should fail validation with clear error message

### Verification
After implementation:
1. Run `make test` - all tests must pass
2. Run `make lint-fix` - ensure code follows standards
3. Run `make verify` - comprehensive verification
4. Optionally test E2E with actual NodePool creation
