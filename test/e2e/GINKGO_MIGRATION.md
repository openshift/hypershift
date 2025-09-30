# Ginkgo Migration for create_cluster_test.go - Pilot Implementation

## Summary

Pilot migration demonstrating path to eliminate multiple test failure reports by removing `t.Run()` calls and using Ginkgo.

**Status:** 🚧 WIP - Framework complete, test integration blocked
**Impact (when complete):** 70+ subtests → ~5-10 subtests

## Problem

Original test creates 70+ subtests through `t.Run()` in:
1. Framework methods (5 subtests)
2. Validation functions in test (10+ subtests)
3. Validation functions in after() (20+ subtests)

## What's Fixed ✅

### Framework (100%)
- ✅ `test/e2e/framework/hypershift.go` - All `t.Run()` removed

### Validation Functions (20%)
- ✅ `EnsureAPIUX` - Fully migrated
- ✅ `EnsureCustomLabels` - Fully migrated
- ✅ `EnsureCustomTolerations` - Fully migrated
- ✅ `EnsureAppLabel` - Fully migrated
- ✅ `EnsureFeatureGateStatus` - Fully migrated
- ⚠️ `EnsureKubeAPIDNSNameCustomCert` - TODO (150+ lines)
- ⚠️ `EnsureDefaultSecurityGroupTags` - TODO (50+ lines)
- ⚠️ `EnsureKubeAPIServerAllowedCIDRs` - TODO (40+ lines)
- ⚠️ `EnsureGlobalPullSecret` - TODO (200+ lines)

## Files

### Created
1. `test/e2e/framework/hypershift.go` - Ginkgo-enabled framework (compiles ✅)
2. `test/e2e/framework/validations.go` - Ginkgo validation functions (compiles ✅)
3. `test/e2e/framework/util_wrappers.go` - Type-safe wrappers for e2eutil helpers (compiles ✅)
4. `test/e2e/ginkgo/create_cluster_test.go` - Ginkgo test (WIP 🚧)
5. `test/e2e/ginkgo/create_cluster_suite_test.go` - Ginkgo suite (WIP 🚧)

### Modified
1. `test/e2e/e2e_test.go` - Accessors (reverted - different approach needed)
2. `test/e2e/util/fixture.go` - Export wrappers (✅)

## Current Blockers

### 1. Type Incompatibility: `GinkgoT()` vs `*testing.T`
**Problem:** Many e2eutil functions expect `*testing.T` concrete type, but Ginkgo provides `Ginkgo TInterface` interface.

**Example:**
```go
// e2eutil function signature
func WaitForGuestClient(t *testing.T, ...) crclient.Client

// Ginkgo test tries to pass:
e2eutil.WaitForGuestClient(GinkgoT(), ...) // ❌ Type error
```

**Solutions explored:**
- ✅ Created `util_wrappers.go` with Ginkgo-compatible duplicates of small helpers
- ❌ Can't create `*testing.T` from `GinkgoTInterface` (impossible in Go)
- 🚧 Need to either:
  1. Duplicate all used e2eutil functions in framework package
  2. Change e2eutil to accept `testing.TB` interface (breaks existing tests)
  3. Use unsafe type casting (not recommended)

### 2. Global State Initialization
**Problem:** Ginkgo suite needs access to e2e package's global `Options` and `context.Context`, but they're in `e2e_test.go` (test-only package).

**Current approach:** Initialize fresh `Options` in Ginkgo suite - needs full flag parsing implementation.

## Next Steps

1. **Immediate:** Decide on approach for `*testing.T` compatibility
   - Recommendation: Duplicate necessary e2eutil functions in framework package for pilot
2. **Short-term:** Implement options initialization in Ginkgo suite
3. **Medium-term:** Make the Ginkgo test compile and run
4. **Long-term:**
   - Migrate remaining validation functions (4 TODO items)
   - Migrate other e2e test files
   - Consider refactoring e2eutil to use `testing.TB` interface

## Run

**Note:** Test does not currently compile. Fix blockers above first.

```bash
# When ready:
ginkgo -v ./test/e2e/ginkgo --tags=e2e --focus="CreateCluster"
```
