# Test Plan: PR #8454 — OCPBUGS-85192: Add retry logic for Azure DNS cleanup in `hcp destroy`

## PR Summary

**Title:** OCPBUGS-85192: Add retry logic for Azure DNS cleanup in hcp destroy
**Labels:** area/platform/azure, area/cli, jira/valid-bug

### Key Changes

- **Retry loop** with `wait.PollUntilContextCancel` for Azure resource cleanup when using `--preserve-resource-group`, mirroring the AWS destroy path's retry pattern
- **`resourceDeleter` interface** abstracting `ListByResourceGroup` + `DeleteByID` for testability
- **`NonRetriable` error detection** via structural typing — stops retrying on permanent failures (auth errors, invalid requests)
- **Error propagation** — deletion failures are now returned as joined errors instead of being silently swallowed
- **`--azure-infra-grace-period` flag** (default 5m) controlling retry timeout
- **Deletion order improvements** — subnets now correctly ordered before virtual networks; uses `slices.SortFunc` instead of manual bubble sort
- **`getResourceGroupName` unexported** (was `GetResourceGroupName`)

### Changed Files

| File | Change |
|------|--------|
| `cmd/infra/azure/destroy.go` | Core implementation: interface, retry loop, error propagation, deletion order |
| `cmd/infra/azure/destroy_test.go` | 19 unit tests (6 new retry tests, plus migrated to gomega) |
| `cmd/cluster/azure/destroy.go` | Wire `--azure-infra-grace-period` flag |
| `cmd/cluster/core/destroy.go` | Add `AzureInfraGracePeriod` field to `AzurePlatformDestroyOptions` |

## Prerequisites

### Infrastructure Requirements

- **Azure subscription** with contributor access
- **Azure credentials file** (`azure-creds`) with service principal or workload identity
- **Management cluster** (AWS or Azure) with HyperShift operator installed
- **DNS zone resource group** (`dns-zone-rg-name`) for the base domain
- **Custom resource group** (recommended) to test `--preserve-resource-group` path

### Tools & Versions

- `hcp` CLI built from PR branch (`make build` or `make hypershift`)
- `az` CLI (Azure CLI) for manual resource verification
- `kubectl` / `oc` for cluster management
- `jq` for JSON parsing

### Environment Setup

```bash
# Set common variables
export AZURE_CREDS="/path/to/azure-credentials.json"
export CLUSTER_NAME="dns-retry-test"
export INFRA_ID="<will be set after cluster creation>"
export LOCATION="eastus"
export RESOURCE_GROUP_NAME="${CLUSTER_NAME}-custom-rg"
export DNS_ZONE_RG="<your-dns-zone-resource-group>"
export BASE_DOMAIN="<your-base-domain>"

# Build the CLI from PR branch
git checkout investigate-azure-cli
make build
```

> **IMPORTANT:** Every step in every scenario must include explicit verification. Do not proceed to the next step until the current step's verification passes.

### Safety: Retry Loop and Process Management

The `--preserve-resource-group` path enters a retry loop that polls every 10 seconds for up to 5 minutes (default). When running destroy commands that will fail (e.g., invalid/dummy credentials, nonexistent resources), **always pass `--azure-infra-grace-period 0s`** to prevent the command from blocking for the full grace period.

**Do NOT use `pkill -f` with broad patterns** like `pkill -f "hypershift destroy"` to kill stuck commands. The `-f` flag matches against full command-line strings, which can match the calling process (e.g., a CI agent, test runner, or shell) if the command string appears in its arguments. Use `timeout` to bound command execution instead:

```bash
# SAFE: use timeout to bound execution
timeout 30s bin/hypershift destroy infra azure ... 2>&1

# SAFE: use --azure-infra-grace-period 0s to prevent retries
bin/hypershift destroy infra azure ... --azure-infra-grace-period 0s 2>&1

# DANGEROUS: pkill -f can match and kill the calling process
pkill -f "hypershift destroy infra azure"  # DO NOT USE
```

For scenarios that validate flag parsing or help text (no real Azure calls), use `--help` or `--azure-infra-grace-period 0s`. For scenarios against real infrastructure, the default 5m grace period is appropriate since the retry loop will exit as soon as cleanup succeeds.

## Test Scenarios

### Scenario 1: Baseline — `hcp destroy` without `--preserve-resource-group`

**Purpose:** Verify no regression on the default (non-preserve) destroy path.

**Steps:**

1. Create an Azure HyperShift cluster:
   ```bash
   bin/hcp create cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --location ${LOCATION} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --base-domain ${BASE_DOMAIN} \
     --release-image <OCP_RELEASE_IMAGE>
   ```
   **Verify:** Command exits 0. HostedCluster resource exists:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.metadata.name}'
   # Expected: dns-retry-test
   ```

2. Wait for the cluster to become available:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -w
   ```
   **Verify:** HostedCluster status shows `Available=True`:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.status.conditions[?(@.type=="Available")].status}'
   # Expected: True
   ```

3. Record resource groups before destroy:
   ```bash
   export INFRA_ID=$(oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.spec.infraID}')
   az group list --query "[?starts_with(name, '${CLUSTER_NAME}')]" -o table
   ```
   **Verify:** At least the main resource group (`${CLUSTER_NAME}-${INFRA_ID}`) exists. Note all resource group names for post-destroy verification.

4. Destroy without preserve flag:
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG}
   ```
   **Verify:** Command exits 0. Logs do NOT contain `"Starting cluster resource cleanup with retry"` (this message only appears in the `--preserve-resource-group` path).

5. Verify all resource groups are deleted:
   ```bash
   az group list --query "[?starts_with(name, '${CLUSTER_NAME}')]" -o table
   ```
   **Verify:** Returns empty — all resource groups (`{name}-{infraID}`, `{name}-vnet-{infraID}`, `{name}-nsg-{infraID}`) are gone.

6. Verify the HostedCluster resource is removed:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} 2>&1
   ```
   **Verify:** Returns `NotFound` error.

---

### Scenario 2: `--preserve-resource-group` with clean resources (happy path)

**Purpose:** Verify the retry loop works end-to-end when all resources delete successfully on first pass.

**Steps:**

1. Create a custom resource group:
   ```bash
   az group create --name ${RESOURCE_GROUP_NAME} --location ${LOCATION}
   ```
   **Verify:** Resource group exists:
   ```bash
   az group show --name ${RESOURCE_GROUP_NAME} --query "properties.provisioningState" -o tsv
   # Expected: Succeeded
   ```

2. Create an Azure HyperShift cluster using the custom resource group:
   ```bash
   bin/hcp create cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --location ${LOCATION} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --base-domain ${BASE_DOMAIN} \
     --resource-group-name ${RESOURCE_GROUP_NAME} \
     --release-image <OCP_RELEASE_IMAGE>
   ```
   **Verify:** Command exits 0. HostedCluster resource exists:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.metadata.name}'
   # Expected: dns-retry-test
   ```

3. Wait for the cluster to become available and record the infra ID:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -w
   export INFRA_ID=$(oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.spec.infraID}')
   ```
   **Verify:** HostedCluster status shows `Available=True` and infra ID is non-empty:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.status.conditions[?(@.type=="Available")].status}'
   # Expected: True
   echo "InfraID: ${INFRA_ID}"
   # Expected: non-empty string
   ```

4. List resources before destroy — record the resource list for comparison:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} -o table
   ```
   **Verify:** Cluster-specific resources exist (resources containing `${INFRA_ID}` or matching `${CLUSTER_NAME}-azurecluster.*`):
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
   # Expected: At least one resource listed
   ```

5. Destroy with preserve flag:
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group 2>&1 | tee /tmp/destroy-output.log
   ```
   **Verify:** Command exits 0. Check log contains expected messages:
   ```bash
   grep "Starting cluster resource cleanup with retry" /tmp/destroy-output.log
   # Expected: Match found — retry loop was entered
   grep "grace-period" /tmp/destroy-output.log
   # Expected: grace-period=5m0s (default)
   grep "Successfully cleaned up cluster resources, resource group preserved" /tmp/destroy-output.log
   # Expected: Match found — cleanup completed successfully
   ```

6. Verify resource group still exists:
   ```bash
   az group show --name ${RESOURCE_GROUP_NAME} --query "properties.provisioningState" -o tsv
   ```
   **Verify:** Returns `Succeeded` — resource group was preserved.

7. Verify no cluster-specific resources remain:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
   ```
   **Verify:** Returns empty — all cluster resources were deleted.

8. Verify additional resource groups (vnet, nsg) were deleted:
   ```bash
   az group show --name "${CLUSTER_NAME}-vnet-${INFRA_ID}" 2>&1
   az group show --name "${CLUSTER_NAME}-nsg-${INFRA_ID}" 2>&1
   ```
   **Verify:** Both return `ResourceGroupNotFound` — additional resource groups are always deleted regardless of `--preserve-resource-group`.

---

### Scenario 3: `--preserve-resource-group` with DNS zone + active vnet links (retry scenario)

**Purpose:** This is the core bug scenario from OCPBUGS-85192. When virtualNetworkLinks are being deleted asynchronously, the first DNS zone deletion attempt fails. The retry loop should re-list resources and succeed on a subsequent pass.

**Steps:**

1. Create a cluster in a custom resource group (same setup as Scenario 2 steps 1-3).
   **Verify:** Same as Scenario 2 steps 1-3 — cluster is available, infra ID is set.

2. Verify DNS resources exist before destroying:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?type=='Microsoft.Network/privateDnsZones' || type=='Microsoft.Network/privateDnsZones/virtualNetworkLinks']" -o table
   ```
   **Verify:** At least one privateDnsZone and one virtualNetworkLink are listed. Record the count for comparison.

3. Verify deletion order expectations — list resources with types:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}')].[name, type]" -o table
   ```
   **Verify:** Note which resource types exist. virtualNetworkLinks should be deleted before privateDnsZones (priority 1 vs 10 in the deletion order).

4. Destroy with preserve flag, capturing output:
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group 2>&1 | tee /tmp/destroy-output.log
   ```
   **Verify:** Command exits 0.

5. Verify the retry loop started:
   ```bash
   grep "Starting cluster resource cleanup with retry" /tmp/destroy-output.log
   ```
   **Verify:** Match found.

6. Verify resources were marked for deletion in correct order:
   ```bash
   grep "Marking cluster resource for deletion" /tmp/destroy-output.log
   ```
   **Verify:** virtualNetworkLinks appear in the log before privateDnsZones. Each cluster-specific resource from step 3 is listed.

7. Check whether retries occurred:
   ```bash
   grep -c "will retry" /tmp/destroy-output.log
   ```
   **Verify:** Count >= 0 (may succeed on first pass if Azure-side deletion is fast). If retries did occur, also check:
   ```bash
   grep "Error during cluster resource cleanup, will retry" /tmp/destroy-output.log
   # Expected: Shows the transient error (e.g., "child resources still exist")
   ```

8. Verify cleanup summary:
   ```bash
   grep "Cluster resource cleanup summary" /tmp/destroy-output.log
   ```
   **Verify:** `resources-deleted` count matches the number of cluster-specific resources from step 3. `errors` count is 0.

9. Verify completion message:
   ```bash
   grep "Successfully cleaned up cluster resources, resource group preserved" /tmp/destroy-output.log
   ```
   **Verify:** Match found.

10. Verify no orphaned resources remain:
    ```bash
    az resource list --resource-group ${RESOURCE_GROUP_NAME} \
      --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
    ```
    **Verify:** Returns empty — no cluster-specific resources remain.

11. Verify no orphaned DNS zones or vnet links:
    ```bash
    az network private-dns zone list \
      --resource-group ${RESOURCE_GROUP_NAME} \
      --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
    ```
    **Verify:** Returns empty.

---

### Scenario 4: `--azure-infra-grace-period` flag validation

**Purpose:** Verify the new flag works correctly with various values.

**Steps:**

1. **Negative value — should fail validation:**
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group \
     --azure-infra-grace-period -1s 2>&1
   ```
   **Verify:** Command exits non-zero. Error output contains `"azure-infra-grace-period must be >= 0"`.

2. **Verify flag appears in help text for all command paths:**
   ```bash
   bin/hcp destroy cluster azure --help 2>&1 | grep "azure-infra-grace-period"
   bin/hcp destroy infra azure --help 2>&1 | grep "azure-infra-grace-period"
   bin/hypershift destroy infra azure --help 2>&1 | grep "azure-infra-grace-period"
   ```
   **Verify:** All three commands show the `--azure-infra-grace-period` flag with description matching `AzureInfraGracePeriodDescription`.

3. **Zero value — single attempt, no retries (run against real infrastructure only):**

   > **WARNING:** This step requires a real cluster from a prior scenario. Do NOT run destroy commands with `--preserve-resource-group` against dummy credentials or nonexistent resources without `--azure-infra-grace-period 0s` — the retry loop will block for the full grace period (default 5 minutes) retrying auth failures every 10 seconds.

   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group \
     --azure-infra-grace-period 0s 2>&1 | tee /tmp/destroy-zero-grace.log
   ```
   **Verify:** Log shows the zero value. If deletion succeeds on first pass, command exits 0. If deletion fails, command exits non-zero with a timeout error (context expires immediately after the first immediate poll):
   ```bash
   grep "grace-period" /tmp/destroy-zero-grace.log
   # Expected: grace-period=0s
   ```

4. **Custom and default values (verify via log output from real-infrastructure scenarios):**

   Rather than running separate destroy commands for each value, verify grace period parsing from the log output of other scenarios:

   - **Default (5m):** Check logs from Scenario 2 or 3 (which use the default):
     ```bash
     grep "grace-period" /tmp/destroy-output.log
     # Expected: grace-period=5m0s
     ```
   - **Custom (2m):** Check logs from Scenario 5 (which uses `--azure-infra-grace-period 2m`):
     ```bash
     grep "grace-period" /tmp/destroy-infra-output.log
     # Expected: grace-period=2m0s
     ```

   **Verify:** Each log shows the expected grace period value.

---

### Scenario 5: `hcp infra destroy azure` (direct infra destroy command)

**Purpose:** Verify the flag is also wired in the `hcp infra destroy azure` command path.

**Steps:**

1. Ensure a cluster exists with a known infra ID (from a previous create, or set up a new one).
   **Verify:** `${INFRA_ID}` is set and non-empty.

2. List resources before destroy:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}')]" -o table
   ```
   **Verify:** Cluster-specific resources exist.

3. Run with preserve and custom grace period:
   ```bash
   bin/hypershift destroy infra azure \
     --name ${CLUSTER_NAME} \
     --infra-id ${INFRA_ID} \
     --azure-creds ${AZURE_CREDS} \
     --preserve-resource-group \
     --azure-infra-grace-period 2m 2>&1 | tee /tmp/destroy-infra-output.log
   ```
   **Verify:** Command exits 0.

4. Verify retry loop used the custom grace period:
   ```bash
   grep "grace-period" /tmp/destroy-infra-output.log
   ```
   **Verify:** Shows `grace-period=2m0s`.

5. Verify retry log messages appear (same pattern as Scenario 2):
   ```bash
   grep "Starting cluster resource cleanup with retry" /tmp/destroy-infra-output.log
   grep "Successfully cleaned up cluster resources, resource group preserved" /tmp/destroy-infra-output.log
   ```
   **Verify:** Both messages appear.

6. Verify resource group preserved and cluster resources deleted:
   ```bash
   az group show --name ${RESOURCE_GROUP_NAME} --query "properties.provisioningState" -o tsv
   # Expected: Succeeded
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}')]" -o table
   # Expected: empty
   ```
   **Verify:** Resource group exists, no cluster-specific resources remain.

---

### Scenario 6: Repeated create/destroy cycles (regression test for original bug)

**Purpose:** The original bug manifests after repeated create/destroy cycles — orphaned DNS zones accumulate. Verify this no longer happens.

**Steps:**

1. Create a custom resource group:
   ```bash
   az group create --name ${RESOURCE_GROUP_NAME} --location ${LOCATION}
   ```
   **Verify:** Resource group exists with `provisioningState=Succeeded`.

2. **Cycle 1 — Create:**
   ```bash
   bin/hcp create cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --location ${LOCATION} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --base-domain ${BASE_DOMAIN} \
     --resource-group-name ${RESOURCE_GROUP_NAME} \
     --release-image <OCP_RELEASE_IMAGE>
   ```
   **Verify:** Cluster is available (`Available=True`). Record infra ID.

3. **Cycle 1 — Destroy:**
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group 2>&1 | tee /tmp/destroy-cycle1.log
   ```
   **Verify:** Command exits 0. Check no orphaned resources:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}')]" -o table
   # Expected: empty
   az network private-dns zone list \
     --resource-group ${RESOURCE_GROUP_NAME} -o table
   # Expected: no cluster-specific DNS zones remain
   ```

4. **Cycle 2 — Create** (new cluster in same resource group):
   ```bash
   bin/hcp create cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --location ${LOCATION} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --base-domain ${BASE_DOMAIN} \
     --resource-group-name ${RESOURCE_GROUP_NAME} \
     --release-image <OCP_RELEASE_IMAGE>
   ```
   **Verify:** Cluster is available. Record new infra ID (will differ from cycle 1).

5. **Cycle 2 — Destroy:**
   ```bash
   export INFRA_ID=$(oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.spec.infraID}')
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group 2>&1 | tee /tmp/destroy-cycle2.log
   ```
   **Verify:** Command exits 0. Check no orphaned resources from either cycle:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} -o table
   # Expected: no cluster-specific resources from cycle 1 OR cycle 2
   az network private-dns zone list \
     --resource-group ${RESOURCE_GROUP_NAME} -o table
   # Expected: no orphaned DNS zones from either cycle
   ```

6. **Cycle 3 — Repeat** steps 4-5 once more.
   **Verify:** Same checks as cycle 2. No accumulation of orphaned resources across any cycle.

7. Final verification — count all remaining resources in the group:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} --query "length(@)"
   ```
   **Verify:** Count equals only the non-cluster resources (if any were pre-existing). No DNS zones, vnet links, or other cluster resources from any cycle remain.

---

### Scenario 7: Private topology cluster

**Purpose:** Verify retry works correctly for private Azure topology clusters, which have additional DNS resources (PLS, Private Endpoint, additional DNS zones and vnet links).

**Steps:**

1. Create a custom resource group:
   ```bash
   az group create --name ${RESOURCE_GROUP_NAME} --location ${LOCATION}
   ```
   **Verify:** Resource group exists with `provisioningState=Succeeded`.

2. Create a private Azure HyperShift cluster:
   ```bash
   bin/hcp create cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --location ${LOCATION} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --base-domain ${BASE_DOMAIN} \
     --resource-group-name ${RESOURCE_GROUP_NAME} \
     --endpoint-access Private \
     --release-image <OCP_RELEASE_IMAGE>
   ```
   **Verify:** Command exits 0. HostedCluster resource exists.

3. Wait for the cluster to become available:
   ```bash
   oc get hostedcluster -n clusters ${CLUSTER_NAME} -w
   export INFRA_ID=$(oc get hostedcluster -n clusters ${CLUSTER_NAME} -o jsonpath='{.spec.infraID}')
   ```
   **Verify:** `Available=True`. Infra ID is non-empty.

4. Verify private DNS resources exist (should have more DNS zones/links than public topology):
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?type=='Microsoft.Network/privateDnsZones' || type=='Microsoft.Network/privateDnsZones/virtualNetworkLinks' || type=='Microsoft.Network/privateEndpoints' || type=='Microsoft.Network/privateLinkServices']" -o table
   ```
   **Verify:** Multiple private DNS zones, vnet links, and optionally private endpoints/link services exist. Record the count and types for comparison after destroy.

5. Destroy with preserve flag:
   ```bash
   bin/hcp destroy cluster azure \
     --name ${CLUSTER_NAME} \
     --azure-creds ${AZURE_CREDS} \
     --dns-zone-rg-name ${DNS_ZONE_RG} \
     --preserve-resource-group 2>&1 | tee /tmp/destroy-private-output.log
   ```
   **Verify:** Command exits 0.

6. Verify retry loop entered and completed:
   ```bash
   grep "Starting cluster resource cleanup with retry" /tmp/destroy-private-output.log
   grep "Successfully cleaned up cluster resources, resource group preserved" /tmp/destroy-private-output.log
   ```
   **Verify:** Both messages appear.

7. Verify deletion order in logs — vnet links deleted before DNS zones:
   ```bash
   grep "Deleting cluster resource" /tmp/destroy-private-output.log
   ```
   **Verify:** Resources of type `virtualNetworkLinks` appear before `privateDnsZones` in the deletion sequence.

8. Verify resource group preserved:
   ```bash
   az group show --name ${RESOURCE_GROUP_NAME} --query "properties.provisioningState" -o tsv
   ```
   **Verify:** Returns `Succeeded`.

9. Verify no orphaned cluster resources remain:
   ```bash
   az resource list --resource-group ${RESOURCE_GROUP_NAME} \
     --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
   ```
   **Verify:** Returns empty — all cluster-specific resources (including private topology resources) are cleaned up.

10. Verify no orphaned private DNS resources:
    ```bash
    az network private-dns zone list \
      --resource-group ${RESOURCE_GROUP_NAME} \
      --query "[?contains(name, '${INFRA_ID}') || starts_with(name, '${CLUSTER_NAME}-azurecluster')]" -o table
    ```
    **Verify:** Returns empty.

## Regression Testing

### Related Features to Verify

- [ ] `hcp destroy cluster azure` without `--preserve-resource-group` still works (Scenario 1)
- [ ] Additional resource groups (vnet, nsg) are still deleted regardless of `--preserve-resource-group`
- [ ] `hcp create cluster azure --resource-group-name` validation (verifies resource group exists)
- [ ] RBAC cleanup still runs before infra destruction (`destroyPlatformSpecifics` calls `rbacManager.CleanupRoleAssignments` before `destroyInfraOptions.Run`)
- [ ] `hcp infra destroy azure` direct command path (Scenario 5)

### Unit Test Verification

```bash
# Run all azure infra destroy tests
go test -v -count=1 ./cmd/infra/azure/... 2>&1 | tail -30
# Verify: 19 tests pass (PASS), 0 failures

# Run just the new retry tests
go test -v -count=1 -run 'TestRetryDeleteClusterResources' ./cmd/infra/azure/...
# Verify: 6 subtests pass

# Run just the deletion order tests
go test -v -count=1 -run 'TestSortResourcesByDeletionOrder' ./cmd/infra/azure/...
# Verify: 3 subtests pass

# Run validation tests
go test -v -count=1 -run 'TestDestroyValidate' ./cmd/infra/azure/...
# Verify: 2 subtests pass (negative grace period rejected, zero accepted)

# Full build + lint + verify
make verify
# Verify: exits 0 with no lint errors and all checks passing
```

### CI Jobs

- [ ] `/test e2e-aks` — Azure AKS-based E2E (general regression). **Verify:** Job passes.
- [ ] `/test e2e-azure-v2-self-managed` — Azure self-managed E2E (if available). **Verify:** Job passes.
- [ ] Standard presubmit jobs should pass. **Verify:** All required checks green on the PR.

## Notes

- The retry loop uses `wait.PollUntilContextCancel` with `immediate=true`, meaning it attempts the first deletion pass immediately without waiting for the retry interval
- The `resourceDeleter` interface encapsulates both `BeginDeleteByID` and `PollUntilDone` into a single `DeleteByID` call, simplifying the retry logic
- The `nonRetriableError` interface is defined locally to avoid importing the Azure SDK's internal `errorinfo` package — it matches the same structural typing pattern
- The `retryInterval` field is test-only (unexported, zero-value means use `defaultRetryInterval` of 10s)
- Deletion order now includes subnets (priority 7), inserted between disks (6) and NSGs (8)
- `GetResourceGroupName` was unexported to `getResourceGroupName` — verify no external callers existed (it was only used internally)
