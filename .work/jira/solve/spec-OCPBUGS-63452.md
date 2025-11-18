# Implementation Specification: OCPBUGS-63452

## Problem Summary

Control plane deployments created by the control-plane-operator (including capi-provider, cluster-api, and cloud-controller-manager-*) do not have finalizers to protect against accidental deletion. These deployments manage CAPI resources (MachineDeployment, Machine, platform-specific machines) that DO have finalizers requiring their controllers to process cleanup operations.

If a deployment like capi-provider is accidentally deleted before HostedCluster deletion:
1. The deployment is deleted immediately (no finalizer protection)
2. The CAPI provider controller stops running
3. During HostedCluster deletion, CAPI resources are marked for deletion
4. CAPI resource finalizers cannot be processed (controller is gone)
5. Cloud resources (EC2 instances, VMs, disks, NICs, load balancers) are orphaned
6. CAPI resources stuck in Terminating state indefinitely

## Affected Components

### Critical Components
- `capi-provider` - Manages platform machines (AWSMachine, AzureMachine, etc.)
- `cluster-api` - Manages MachineDeployment, MachineSet, Machine

### Potentially Affected Components
- `cloud-controller-manager-*` (AWS, Azure, OpenStack, KubeVirt, PowerVS)
- `autoscaler`
- `karpenter`/`karpenter-operator`

## Implementation Approach

### Design Decision: Framework-Level vs Component-Level

After analyzing the codebase, there are two possible approaches:

#### Option 1: Framework-Level Implementation (RECOMMENDED)
- Add finalizer logic at the `controlPlaneWorkload` level in `support/controlplane-component/controlplane-component.go`
- Automatically applies to ALL deployments created through the framework
- Consistent behavior across all components
- Minimal code changes
- Easier to maintain and test

#### Option 2: Component-Level Implementation
- Add finalizer logic in each component's `NewComponent()` function
- Requires changes in multiple files
- Risk of missing components
- More complex testing

#### Recommendation: Option 1
Framework-level implementation for consistency and maintainability.

### Implementation Strategy

The solution will add finalizer protection to deployments at the framework level, similar to how the NodePool controller handles finalizers.

#### 1. Define Finalizer Constants

**File:** `support/controlplane-component/controlplane-component.go`

Add a finalizer constant similar to NodePool:
```go
const (
    // WorkloadFinalizer is added to control plane deployments/statefulsets to ensure proper cleanup
    WorkloadFinalizer = "hypershift.openshift.io/control-plane-workload-finalizer"
)
```

#### 2. Add Finalizer on Creation/Update

**File:** `support/controlplane-component/controlplane-component.go`

Modify the `reconcileWorkload()` method to:
1. Check if the workload object is being deleted
2. If being deleted, wait for pods to terminate before removing finalizer
3. If not being deleted, ensure finalizer is present

**Rationale:**
- Finalizer ensures the deployment won't be deleted until its pods are properly cleaned up
- This gives controllers time to process their own finalizers on managed resources
- Prevents the race condition where controller pods are deleted before they can clean up their resources

#### 3. Handle Finalizer Removal

When a deployment is marked for deletion:
1. Ensure all pods from the deployment have terminated
2. Only then remove the finalizer
3. Allow the deployment to be deleted

This ensures that:
- Controllers stay running until their pods are gone
- CAPI resources can process their finalizers before controllers stop
- Cloud resources are properly cleaned up

#### 4. Add Special Handling for Critical Components

For CAPI-related components (`capi-provider`, `cluster-api`), add additional logic:
- Before removing the finalizer, verify that dependent CAPI resources are being cleaned up
- This provides extra protection for the most critical components

### Detailed Implementation Steps

#### Step 1: Modify Framework Core
**File:** `support/controlplane-component/controlplane-component.go`

1. Add finalizer constant at the top of the file
2. Modify `reconcileWorkload()` to add finalizer logic before deployment creation/update
3. Add new method `handleWorkloadDeletion()` to process deletion and finalizer removal
4. Modify the `Reconcile()` method to call deletion handler when deletion timestamp is set

#### Step 2: Add Finalizer Management Logic

Add these helper functions to `controlplane-component.go`:
- `ensureWorkloadFinalizer()` - Adds finalizer if not present
- `handleWorkloadDeletion()` - Processes deletion and removes finalizer when safe
- `canRemoveWorkloadFinalizer()` - Checks if it's safe to remove finalizer (pods terminated)

#### Step 3: Update Component Tests

**File:** `support/controlplane-component/controlplane-component_test.go`

Add tests for:
- Finalizer is added to deployments on creation
- Finalizer prevents immediate deletion
- Finalizer is removed after pods terminate
- Deletion completes after finalizer removal

#### Step 4: Add Integration Tests (Optional but Recommended)

Consider adding e2e tests that:
- Create a HostedCluster
- Attempt to delete capi-provider deployment
- Verify it doesn't delete until pods are gone
- Verify CAPI resources can still be cleaned up

## Implementation Details

### Finalizer Logic Flow

```
1. Deployment Created/Updated:
   ├─ Check if finalizer exists
   ├─ If not, add WorkloadFinalizer
   └─ Apply deployment

2. Deployment Marked for Deletion:
   ├─ Check DeletionTimestamp
   ├─ If set, enter deletion mode:
   │  ├─ List all pods for this deployment
   │  ├─ Check if all pods are terminated
   │  ├─ If yes:
   │  │  ├─ Remove WorkloadFinalizer
   │  │  └─ Allow deletion to proceed
   │  └─ If no:
   │     └─ Requeue and wait for pods to terminate
   └─ Continue normal reconciliation
```

### Why This Solves the Problem

1. **Prevents Immediate Deletion**: Finalizer ensures deployment isn't removed from etcd immediately
2. **Keeps Controllers Running**: While finalizer is present, the deployment exists and pods keep running
3. **Allows Resource Cleanup**: CAPI resources can be marked for deletion and their finalizers processed by the still-running controllers
4. **Safe Removal**: Only after pods terminate (meaning controllers have stopped and done their work) is the finalizer removed

### Edge Cases to Handle

1. **Force Deletion**: If someone uses `kubectl delete --force`, the finalizer won't help. Document this limitation.
2. **Namespace Deletion**: If the entire namespace is deleted, finalizers may not process. This is expected Kubernetes behavior.
3. **Stuck Finalizers**: Add timeout logic to prevent finalizers from blocking indefinitely (though this should be rare).

### Testing Strategy

1. **Unit Tests**:
   - Test finalizer is added on deployment creation
   - Test finalizer prevents deletion
   - Test finalizer is removed when pods terminate

2. **Integration Tests** (if time permits):
   - Create HostedCluster
   - Delete capi-provider deployment manually
   - Verify it doesn't delete immediately
   - Verify CAPI resources can clean up
   - Verify deployment eventually deletes

3. **Manual Testing**:
   - Deploy a test HostedCluster
   - Attempt to delete capi-provider deployment
   - Verify behavior matches expectations

## Files to Modify

### Core Implementation
1. `support/controlplane-component/controlplane-component.go`
   - Add finalizer constant
   - Add finalizer management methods
   - Modify reconcileWorkload to handle finalizers

### Tests
2. `support/controlplane-component/controlplane-component_test.go`
   - Add unit tests for finalizer logic

### Documentation (Optional)
3. `docs/content/how-to/control-plane-deployment-protection.md` (new file)
   - Document the finalizer behavior
   - Explain what happens on accidental deletion
   - Provide troubleshooting guidance

## Acceptance Criteria

✅ Critical deployments (capi-provider, cluster-api) have finalizers to prevent accidental deletion
✅ Finalizers ensure deployments wait for pods to terminate before being deleted
✅ Cloud resources are properly deleted when HostedCluster is deleted (existing behavior preserved)
✅ No orphaned cloud infrastructure after normal deletion flow
✅ Unit tests validate finalizer behavior
✅ Implementation passes `make lint-fix` and `make verify`

## Alternative Approaches Considered

### Alternative 1: Pod Disruption Budgets (PDBs)
- **Pros**: Native Kubernetes resource protection
- **Cons**: Doesn't prevent deletion, only limits disruption during voluntary evictions. Wouldn't solve the accidental deletion problem.

### Alternative 2: ValidatingWebhook
- **Pros**: Could reject deletion attempts
- **Cons**:
  - More complex infrastructure (webhook server, TLS certs)
  - Harder to override in legitimate cases
  - Finalizers are simpler and more standard

### Alternative 3: RBAC Restrictions
- **Pros**: Prevent unauthorized deletions
- **Cons**:
  - Doesn't help with accidental deletion by authorized users
  - Doesn't address the fundamental race condition
  - Users may need to delete deployments for debugging

## Risks and Mitigations

### Risk 1: Finalizer Blocks Deletion Indefinitely
**Mitigation**:
- Add timeout logic (e.g., after 5 minutes, remove finalizer anyway)
- Add logging to explain why deletion is blocked
- Document how to force removal if needed

### Risk 2: Breaking Existing Deletion Flows
**Mitigation**:
- Extensive testing of normal HostedCluster deletion
- Verify CI/CD pipelines still work
- Add feature flag to disable if issues arise

### Risk 3: Performance Impact
**Mitigation**:
- Finalizer logic is lightweight (just checking pod count)
- Only runs during deletion, not normal operation
- No expected performance impact

## Implementation Timeline

1. **Day 1**: Implement finalizer logic in framework (2-3 hours)
2. **Day 1-2**: Write unit tests (1-2 hours)
3. **Day 2**: Run verification and fix any issues (1 hour)
4. **Day 2**: Manual testing with test cluster (1-2 hours)
5. **Day 2-3**: Code review and iteration
6. **Day 3**: Merge and monitor

**Total Estimated Time**: 1-2 days

## Rollout Plan

1. Merge PR to main branch
2. Deploy to staging/test environment
3. Verify behavior with test HostedClusters
4. Monitor for any issues
5. Roll out to production environments

## Success Metrics

- No reports of orphaned cloud resources after deployment deletions
- HostedCluster deletion works as expected
- No increase in support tickets related to stuck deletions
- Zero customer impact from this change

## Open Questions

1. **Should we add finalizers to StatefulSets too?**
   - Currently focusing on Deployments as that's where the issue manifests
   - Can extend to StatefulSets if needed

2. **Should we add extra validation for CAPI-specific components?**
   - Could add checks that MachineDeployments are being cleaned up
   - Might be overly complex for initial implementation
   - Can add in follow-up if needed

3. **How long should we wait before timing out the finalizer?**
   - Suggest 5 minutes as a reasonable default
   - Should be configurable via annotation if needed

4. **Should this be behind a feature flag?**
   - Probably not needed as it's a safety feature
   - Could add annotation to disable per-deployment if needed: `hypershift.openshift.io/disable-workload-finalizer: "true"`

## Appendix: Code References

- NodePool finalizer pattern: `hypershift-operator/controllers/nodepool/nodepool_controller.go:55`
- Deployment component creation: `support/controlplane-component/builder.go:16`
- Workload reconciliation: `support/controlplane-component/controlplane-component.go:291`
- CAPI machine finalizers: `vendor/sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2/awsmachine_types.go:27`
