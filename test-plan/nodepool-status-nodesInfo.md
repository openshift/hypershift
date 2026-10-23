# Test Plan: CNTRLPLANE-2925 — Surface current node version distribution in NodePool status

**PR:** https://github.com/enxebre/hypershift/pull/21
**JIRA:** CNTRLPLANE-2925
**Enhancement**: TBD

## PR Summary

This PR adds `NodePool.Status.NodesInfo.NodeVersions` to provide real-time visibility into the version distribution and health of nodes during rolling upgrades. It introduces:

- New API types (`NodePoolNodesInfo`, `NodeVersion`) on `NodePool.Status`
- Version tracking via `hypershift.openshift.io/release-version` annotation propagated to Machines:
  - **Replace:** annotation set on MachineDeployment template, propagated to Machines at creation by CAPI
  - **InPlace:** annotation set per-Machine by the in-place upgrader (sourced from the token secret) after each node completes its upgrade. NOT set on the MachineSet template to avoid conflicts with per-Machine updates.
- `TokenSecretReleaseVersionKey` added to token secrets so the in-place upgrader uses the NodePool target version (not the HCP version)
- `setNodesInfoStatus` called before the signal conditions loop so `nodesInfo` stays accurate even when later validations (e.g. release image) error and short-circuit the reconcile
- Aggregation logic grouping CAPI Machines by (ocpVersion, kubeletVersion) with ready/unready counts

### Key files changed

| File | Change |
|------|--------|
| `api/hypershift/v1beta1/nodepool_types.go` | New types and annotation constant |
| `hypershift-operator/controllers/nodepool/version.go` | Aggregation logic |
| `hypershift-operator/controllers/nodepool/nodepool_controller.go` | Wiring (setNodesInfoStatus before conditions loop) |
| `hypershift-operator/controllers/nodepool/capi.go` | Propagate release-version annotation via MachineDeployment template (Replace only) |
| `hypershift-operator/controllers/nodepool/token.go` | Store release version in token secret |
| `control-plane-operator/.../inplaceupgrader.go` | Per-node Machine annotation from token secret version |

---

## Prerequisites

### Infrastructure
- Three OCP release images for different versions (must be ascending — y-stream downgrades are blocked):
  - quay.io/openshift-release-dev/ocp-release:4.19.26-x86_64
  - quay.io/openshift-release-dev/ocp-release:4.20.16-x86_64
  - quay.io/openshift-release-dev/ocp-release:4.21.5-x86_64

### Tools
- `oc` CLI
- `hypershift` CLI (built from this branch)
- `jq`
- `watch` (for polling)
- Access to a container registry for custom operator images

### Environment Setup


### Important Notes
- **Y-stream downgrades are blocked** by HyperShift. Test 3R/3I must use only ascending versions (e.g. 4.19 -> 4.20 partial -> 4.21).
- **InPlace upgrades are slower** than Replace — each node must drain, apply config, reboot, and rejoin. Use `maxUnavailable` to speed up testing.
- **10 replicas recommended** for Test 3R/3I to have enough machines visible at each version simultaneously.

---

## Test Scenarios — Replace Strategy

### Test 1R: Steady-state — all nodes at the same version (Replace)

**Objective:** Verify `nodesInfo.nodeVersions` is populated correctly when all nodes are at the same version.

**Steps:**

1. Create a NodePool with `upgradeType: Replace`, 3 replicas at 4.19.26:
2. Wait for nodes to be ready
3. Verify `nodesInfo.nodeVersions` is populated:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.19.26",
    "kubeletVersion": "v1.32.x",
    "readyNodeCount": 3,
    "unreadyNodeCount": 0
  }
]
```
- Single entry with all nodes healthy
- `readyNodeCount` equals `status.replicas`
- `unreadyNodeCount` is 0

4. **Cross-check** Machines have the release-version annotation (propagated from MachineDeployment template):
   ```bash
   oc get machines -n clusters-spot-demo \
     -o jsonpath='{range .items[*]}{.metadata.name}{" release-version="}{.metadata.annotations.hypershift\.openshift\.io/release-version}{"\n"}{end}'
   ```
   All should show `release-version=4.19.26`.

---

### Test 2R: Rolling upgrade — mixed versions mid-rollout (Replace)

**Objective:** Verify `nodesInfo.nodeVersions` shows multiple entries during a rolling upgrade with Replace strategy.

**Precondition:** NodePool from Test 1R is healthy with all nodes at one version.

**Steps:**

1. Trigger a version upgrade:
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.20.16-x86_64"}}}'
   ```

2. **While the rollout is in progress**, poll `nodesInfo.nodeVersions`:
   ```bash
   watch -n5 'oc get nodepool -n clusters <np-name> \
     -o jsonpath="{.status.nodesInfo.nodeVersions}" | jq .'
   ```

**Expected result during rollout:**
```json
[
  {
    "ocpVersion": "4.19.26",
    "kubeletVersion": "v1.32.x",
    "readyNodeCount": 2,
    "unreadyNodeCount": 0
  },
  {
    "ocpVersion": "4.20.16",
    "kubeletVersion": "v1.33.x",
    "readyNodeCount": 0,
    "unreadyNodeCount": 1
  }
]
```
- Two entries (old and new version)
- Counts shift as old machines are drained and new ones come up
- New nodes initially appear with `readyNodeCount: 0, unreadyNodeCount: 1` until `NodeHealthy` becomes True

3. **After rollout completes**, verify only one entry remains:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.20.16",
    "kubeletVersion": "v1.33.x",
    "readyNodeCount": 3,
    "unreadyNodeCount": 0
  }
]
```

4. **Verify Machines have the correct release-version annotation:**
   ```bash
   oc get machines -n clusters-spot-demo \
     -o jsonpath='{range .items[*]}{.metadata.name}{" release-version="}{.metadata.annotations.hypershift\.openshift\.io/release-version}{"\n"}{end}'
   ```

---

### Test 3R: Three partial rollouts — version accuracy across interrupted upgrades (Replace)

**Objective:** Verify that `nodesInfo.nodeVersions` correctly reports all 3 versions simultaneously during a sequence of partial rollouts (v1 -> v2 partial -> v3), proving that each Machine carries its actual version via annotation regardless of secret lifecycle.

**Precondition:** Fresh NodePool at 4.19.26 with **10 replicas** (needed to have enough machines visible at each version simultaneously — with 3 replicas the intermediate machines get replaced too quickly).

**Steps:**

1. Create a 10-replica Replace NodePool at 4.19.26 and wait for all nodes to be ready.

2. Trigger upgrade to 4.20.16:
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.20.16-x86_64"}}}'
   ```

3. **Wait until at least 2-3 machines are at 4.20.16**, then immediately trigger 4.21.5:
   ```bash
   # Poll until you see 4.20.16 machines
   watch -n5 'oc get nodepool -n clusters <np-name> \
     -o jsonpath="{.status.nodesInfo.nodeVersions}" | jq .'

   # Once 2+ machines at 4.20.16, trigger the third rollout
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.21.5-x86_64"}}}'
   ```

4. Poll `nodesInfo.nodeVersions` throughout the process:
   ```bash
   watch -n5 'oc get nodepool -n clusters <np-name> \
     -o jsonpath="{.status.nodesInfo.nodeVersions}" | jq .'
   ```

**Expected result during rollout:**
```json
[
  {
    "ocpVersion": "4.19.26",
    "kubeletVersion": "v1.32.x",
    "readyNodeCount": 6,
    "unreadyNodeCount": 0
  },
  {
    "ocpVersion": "4.20.16",
    "kubeletVersion": "v1.33.x",
    "readyNodeCount": 2,
    "unreadyNodeCount": 0
  },
  {
    "ocpVersion": "4.21.5",
    "kubeletVersion": "v1.34.x",
    "readyNodeCount": 2,
    "unreadyNodeCount": 0
  }
]
```
- Up to 3 entries visible simultaneously (4.19.26, 4.20.16, 4.21.5)
- Each Machine carries the version it was created with via the MachineDeployment template annotation — even after the intermediate userdata secrets are cleaned up by the secret janitor
- No machine misreports its version as `nodePool.Status.Version` (which would still be 4.19.26 since no rollout completed)

5. **After final rollout completes**, verify only the target version remains:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.21.5",
    "kubeletVersion": "v1.34.x",
    "readyNodeCount": 10,
    "unreadyNodeCount": 0
  }
]
```

---

## Test Scenarios — InPlace Strategy

### Test 1I: Steady-state — all nodes at the same version (InPlace)

**Objective:** Verify `nodesInfo.nodeVersions` is populated correctly for an InPlace NodePool when all nodes are at the same version.

**Steps:**

1. Create a NodePool with `upgradeType: InPlace` (`--node-upgrade-type InPlace`), 3 replicas at 4.19.26:
2. Wait for nodes to be ready
3. Verify `nodesInfo.nodeVersions` is populated:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.19.26",
    "kubeletVersion": "v1.32.x",
    "readyNodeCount": 3,
    "unreadyNodeCount": 0
  }
]
```
- The `ocpVersion` comes from `nodePool.Status.Version` fallback (Machines have no release-version annotation at creation for InPlace)

4. **Cross-check** Machines do NOT have the release-version annotation (by design — it's only set by the in-place upgrader after upgrades):
   ```bash
   oc get machines -n clusters-spot-demo \
     -o jsonpath='{range .items[*]}{.metadata.name}{" release-version="}{.metadata.annotations.hypershift\.openshift\.io/release-version}{"\n"}{end}'
   ```
   Annotations should be empty.

---

### Test 2I: Rolling upgrade — mixed versions mid-rollout (InPlace)

**Objective:** Verify `nodesInfo.nodeVersions` shows multiple entries during a rolling in-place upgrade, with Machines annotated progressively by the in-place upgrader.

**Precondition:** NodePool from Test 1I is healthy with all nodes at one version.

**Steps:**

1. Trigger a version upgrade:
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.20.16-x86_64"}}}'
   ```

2. **While the rollout is in progress**, poll `nodesInfo.nodeVersions`:
   ```bash
   watch -n5 'oc get nodepool -n clusters <np-name> \
     -o jsonpath="{.status.nodesInfo.nodeVersions}" | jq .'
   ```

**Expected result during rollout:**
```json
[
  {
    "ocpVersion": "4.19.26",
    "kubeletVersion": "v1.32.x",
    "readyNodeCount": 2,
    "unreadyNodeCount": 0
  },
  {
    "ocpVersion": "4.20.16",
    "kubeletVersion": "v1.33.x",
    "readyNodeCount": 1,
    "unreadyNodeCount": 0
  }
]
```
- Two entries (old and new version)
- Machines are annotated one-by-one as each node completes its in-place upgrade
- The annotation is sourced from the token secret (NodePool target version), NOT the HCP version

**Known transient state:** During the MCO upgrade window, you may briefly see a Machine with a new `kubeletVersion` (e.g. `v1.33.x`) but old `ocpVersion` (e.g. `4.19.26`). This happens because the kubelet updates before the MCD state machine completes, and the in-place upgrader only annotates the Machine after full MCD completion. This resolves within seconds.

3. **Verify Machine annotations update progressively:**
   ```bash
   watch -n5 'oc get machines -n clusters-spot-demo \
     -o jsonpath="{range .items[*]}{.metadata.name}{\" release-version=\"}{.metadata.annotations.hypershift\.openshift\.io/release-version}{\"\n\"}{end}"'
   ```
   Machines should go from empty annotation to `4.20.16` one at a time.

4. **After rollout completes**, verify only one entry remains:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.20.16",
    "kubeletVersion": "v1.33.x",
    "readyNodeCount": 3,
    "unreadyNodeCount": 0
  }
]
```

---

### Test 3I: Three partial rollouts — version accuracy across interrupted upgrades (InPlace)

**Objective:** Verify that `nodesInfo.nodeVersions` correctly reports all 3 versions during a sequence of partial in-place rollouts (v1 -> v2 partial -> v3), ensuring intermediate machines report their actual version.

**Precondition:** Fresh NodePool with `upgradeType: InPlace`, **10 replicas** at 4.19.26, all healthy.

**Steps:**

1. Trigger upgrade to 4.20.16 with `maxUnavailable=3` to speed up the in-place rollout:
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.20.16-x86_64"},"management":{"inPlace":{"maxUnavailable":3}}}}'
   ```

2. **Wait until at least 3 machines are annotated with 4.20.16**, then trigger 4.21.5:
   ```bash
   # Poll machine annotations
   watch -n5 'oc get machines -n clusters-spot-demo \
     -o jsonpath="{range .items[*]}{.metadata.annotations.hypershift\.openshift\.io/release-version}{\"\n\"}{end}" | sort | uniq -c'

   # Once 3+ annotated with 4.20.16, trigger the third rollout
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.21.5-x86_64"}}}'
   ```

3. Poll `nodesInfo.nodeVersions` throughout the process:
   ```bash
   watch -n5 'oc get nodepool -n clusters <np-name> \
     -o jsonpath="{.status.nodesInfo.nodeVersions}" | jq .'
   ```

**Expected result during rollout:**
- Up to 3 entries visible (4.19.26, 4.20.16, 4.21.5)
- Machines annotated with 4.20.16 retain that annotation — the in-place upgrader only overwrites it when a node completes the 4.21.5 upgrade
- Machines not yet annotated fall back to `nodePool.Status.Version` (4.19.26)

4. **After final rollout completes**, verify only the target version remains:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
```json
[
  {
    "ocpVersion": "4.21.5",
    "kubeletVersion": "v1.34.x",
    "readyNodeCount": 10,
    "unreadyNodeCount": 0
  }
]
```

---

## Common Test Scenarios (both strategies)

### Test 4: Scale-up — new machines without NodeInfo yet

**Objective:** Verify that machines still being provisioned (no NodeInfo) are excluded from `nodeVersions` counts.

**Steps:**

1. Scale the NodePool up:
   ```bash
   oc -n clusters scale nodepool <nodepool-name> --replicas=5
   ```

2. Immediately check `nodesInfo.nodeVersions` while new machines are still provisioning:
   ```bash
   oc get nodepool -n clusters <nodepool-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
- `nodeVersions` only includes machines that have registered a node (`Machine.Status.NodeInfo` is non-nil)
- The sum of `readyNodeCount + unreadyNodeCount` across entries may be less than `status.replicas` while new machines are spinning up
- Once all machines have healthy nodes, `readyNodeCount` sum should equal `status.replicas`

3. Wait for all nodes to be ready, then verify:
   ```bash
   oc get nodepool -n clusters <nodepool-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq '.[].readyNodeCount'
   ```
   Should output `5`.

---

### Test 5: CRD field presence

**Objective:** Verify the CRD schema includes the new `nodesInfo` field.

**Steps:**

1. Check the CRD schema:
   ```bash
   oc get crd nodepools.hypershift.openshift.io \
     -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.status.properties.nodesInfo}' | jq .
   ```

**Expected result:**
- `nodesInfo` is an object with a `nodeVersions` array property
- Each item has `ocpVersion` (string), `kubeletVersion` (string), `readyNodeCount` (integer), `unreadyNodeCount` (integer)
- `nodeVersions` has `maxItems: 100`, `minItems: 1`

---

### Test 6: Legacy machines — fallback to nodePool.Status.Version

**Objective:** Verify that machines created before this change (without the `hypershift.openshift.io/release-version` annotation) still show up in `nodeVersions` using the fallback to `nodePool.Status.Version`.

**Steps:**

1. If you have a pre-existing NodePool created before this PR was deployed, upgrade the HyperShift operator to the version with this PR.

2. Check `nodesInfo.nodeVersions`:
   ```bash
   oc get nodepool -n clusters <existing-nodepool> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
- All machines should be grouped with `ocpVersion` equal to `nodePool.Status.Version`
- No machines should be missing from the count
- `readyNodeCount + unreadyNodeCount` should equal the total number of provisioned machines

**Note:** This is also the expected behavior for InPlace NodePools at creation — Machines have no annotation until their first in-place upgrade completes.

---

### Test 7: Zero replicas — nodesInfo cleared

**Objective:** Verify `nodesInfo` is cleared when there are no machines.

**Steps:**

1. Scale the NodePool to 0:
   ```bash
   oc -n clusters scale nodepool <nodepool-name> --replicas=0
   ```

2. Wait for all machines to be deleted, then check:
   ```bash
   oc get nodepool -n clusters <nodepool-name> \
     -o jsonpath='{.status.nodesInfo}'
   ```

**Expected result:**
- `nodesInfo` should be empty/nil (no output)
- During scale-down, you should see `readyNodeCount` decrease and `unreadyNodeCount` increase as nodes are drained

---

### Test 8: nodesInfo accuracy during reconciler errors

**Objective:** Verify `nodesInfo` stays accurate even when the NodePool reconciler errors (e.g. invalid release image).

**Steps:**

1. With a healthy NodePool, set an invalid release (e.g. a y-stream downgrade):
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"quay.io/openshift-release-dev/ocp-release:4.19.26-x86_64"}}}'
   ```

2. Check `nodesInfo.nodeVersions`:
   ```bash
   oc get nodepool -n clusters <np-name> \
     -o jsonpath='{.status.nodesInfo.nodeVersions}' | jq .
   ```

**Expected result:**
- `nodesInfo` should still show the correct current state (e.g. 3 ready at the current version)
- It should NOT be stale or empty just because the reconciler is erroring on release validation
- This works because `setNodesInfoStatus` is called before the conditions loop

3. Restore a valid release to unblock the reconciler:
   ```bash
   oc -n clusters patch nodepool <np-name> --type=merge \
     -p '{"spec":{"release":{"image":"<current-version-image>"}}}'
   ```

---

## Regression Testing

- Verify `NodePool.Status.Version` still works correctly (should still reflect the target version only after all machines complete)
- Verify `AllMachinesReady` and `AllNodesHealthy` conditions still report correctly
- Verify NodePool deletion works cleanly (no stuck finalizers from the new status field)
- Verify autoscaling still works (scale up/down events update `nodesInfo` correctly)

---

## Notes

- The `NodesInfo` wrapper struct is intentionally extensible for future fields (e.g., OS image distribution, architecture mix).
- OCP version resolution order: Machine annotation (primary) > `nodePool.Status.Version` fallback (for legacy machines and InPlace machines before first upgrade).
- For Replace upgrades, the annotation is set at Machine creation via the MachineDeployment template — each Machine carries its version permanently regardless of userdata secret lifecycle.
- For InPlace upgrades, the annotation is NOT set on the MachineSet template (to avoid overwriting per-Machine annotations). It is set per-Machine by the in-place upgrader using the version from the token secret, which reflects the NodePool's target release (not the HCP release).
- The `nodeVersions` array is sorted deterministically by `ocpVersion`, then `kubeletVersion`.
- Maximum 100 entries in `nodeVersions` (kubebuilder validation), minimum 1 when present.
- Use **10 replicas** for Test 3R/3I to have enough machines visible at each version simultaneously during partial rollouts.
- Set `maxUnavailable` on InPlace NodePools to speed up in-place upgrade testing.
