# Specification: Fix --image-generation Flag Being Ignored

**JIRA**: OCPBUGS-63613
**Summary**: `--image-generation Gen1` flag is not working even without marketplace flags
**Affected Versions**: 4.20, 4.21
**Priority**: Normal

## Context

When users create a HostedCluster on Azure using the `hypershift create cluster azure` command with the `--image-generation Gen1` flag, the created NodePool incorrectly uses `Gen2` instead of the user-specified `Gen1`.

### Steps to Reproduce

```bash
./hypershift create cluster azure \
  --name "test-cluster" \
  --namespace "clusters" \
  --azure-creds $AZURE_CREDS \
  --location ${LOCATION} \
  --node-pool-replicas 2 \
  --base-domain $PARENT_DNS_ZONE \
  --pull-secret $PULL_SECRET \
  --generate-ssh \
  --release-image ${RELEASE_IMAGE} \
  --external-dns-domain ${DNS_ZONE_NAME} \
  --resource-group-name "${MANAGED_RG_NAME}" \
  --vnet-id "${GetVnetID}" \
  --subnet-id "${GetSubnetID}" \
  --network-security-group-id "${GetNsgID}" \
  --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
  --oidc-issuer-url "${OIDC_ISSUER_URL}" \
  --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
  --assign-service-principal-roles \
  --workload-identities-file ./workload-identities.json \
  --diagnostics-storage-account-type Managed \
  --control-plane-operator-image=quay.io/zhouying7780/hypershift-control-plane:latest \
  --image-generation Gen1  # <-- This flag is being ignored
```

### Expected Result

The NodePool should be created with:
```json
{
  "image": {
    "azureMarketplace": {
      "imageGeneration": "Gen1",
      ...
    }
  }
}
```

### Actual Result

The NodePool is created with:
```json
{
  "image": {
    "azureMarketplace": {
      "imageGeneration": "Gen2",  // Wrong! Should be Gen1
      ...
    }
  }
}
```

## Root Cause Analysis

The issue occurs due to a broken chain of data flow from CLI flag to NodePool spec:

### 1. CLI Flag Parsing (`cmd/nodepool/azure/create.go`)

The `--image-generation` flag is correctly parsed and stored in `AzurePlatformCreateOptions.ImageGeneration` (line 74).

### 2. NodePool Generation from CLI (`cmd/nodepool/azure/create.go:181-214`)

When the `NodePoolPlatform()` function is called, it correctly handles the `ImageGeneration` flag:
- Lines 198-208: If `ImageGeneration` is specified, it's converted to the appropriate enum value and set on the `AzureMarketplaceImage` struct.

**This works correctly for `hypershift create nodepool azure` commands.**

### 3. **THE BUG**: Cluster Creation (`cmd/cluster/azure/create.go:323-349`)

When `hypershift create cluster azure` generates the initial NodePool(s), the `GenerateNodePools()` function has a critical flaw at lines 342-349:

```go
} else {
    // Set Type to AzureMarketplace with nil AzureMarketplace field
    // This signals to the nodepool controller to populate marketplace details from the release payload
    vmImage = hyperv1.AzureVMImage{
        Type:             hyperv1.AzureMarketplace,
        AzureMarketplace: nil,  // <-- BUG: User's --image-generation flag is lost here!
    }
}
```

The code intentionally sets `AzureMarketplace` to `nil` to signal the nodepool controller to populate defaults from the release payload. However, **it completely ignores the user-specified `--image-generation` flag** from `o.NodePoolOpts.ImageGeneration`.

### 4. NodePool Controller Defaulting (`hypershift-operator/controllers/nodepool/azure.go:37-119`)

The `defaultAzureNodePoolImage()` function looks for the `ImageGeneration` field at lines 96-99:

```go
// Determine which Hyper-V generation to use
generation := hyperv1.Gen2 // Default to Gen2
if nodePool.Spec.Platform.Azure.Image.AzureMarketplace != nil &&
    nodePool.Spec.Platform.Azure.Image.AzureMarketplace.ImageGeneration != nil {
    generation = *nodePool.Spec.Platform.Azure.Image.AzureMarketplace.ImageGeneration
}
```

Since `AzureMarketplace` is `nil` (from step 3), this condition is false, and the controller defaults to `Gen2`.

## Acceptance Criteria

1. When `--image-generation Gen1` is specified with `hypershift create cluster azure`, the created NodePool must have `imageGeneration: Gen1`.

2. When `--image-generation Gen2` is specified with `hypershift create cluster azure`, the created NodePool must have `imageGeneration: Gen2`.

3. When `--image-generation` is NOT specified with `hypershift create cluster azure`, the created NodePool should default to `Gen2` (current behavior).

4. The fix should work whether or not marketplace flags (`--marketplace-publisher`, etc.) are provided.

5. The fix should work when creating clusters with multiple availability zones (multiple NodePools).

6. Existing behavior of `hypershift create nodepool azure` should remain unchanged (it already works correctly).

7. Unit tests should verify the correct image generation is set based on the CLI flag.

## Implementation Plan

### Step 1: Fix the `GenerateNodePools()` function in `cmd/cluster/azure/create.go`

**File**: `cmd/cluster/azure/create.go`

**Change** at lines 342-349:

```go
} else {
    // Set Type to AzureMarketplace with minimal AzureMarketplace field
    // This signals to the nodepool controller to populate marketplace details from the release payload
    // while preserving the user's image generation preference
    marketplaceImage := &hyperv1.AzureMarketplaceImage{}

    // Set ImageGeneration if specified by the user
    if o.NodePoolOpts.ImageGeneration != "" {
        switch o.NodePoolOpts.ImageGeneration {
        case "Gen1":
            marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen1)
        case "Gen2":
            marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen2)
        }
    }

    vmImage = hyperv1.AzureVMImage{
        Type:             hyperv1.AzureMarketplace,
        AzureMarketplace: marketplaceImage,
    }
}
```

**Also apply the same fix** at lines 363-405 where NodePools are created for multiple availability zones. The same logic needs to be applied there to ensure consistency.

### Step 2: Add Unit Tests

**File**: `cmd/cluster/azure/create_test.go`

Add tests to verify:
1. When `--image-generation Gen1` is specified, the NodePool has `ImageGeneration: Gen1`
2. When `--image-generation Gen2` is specified, the NodePool has `ImageGeneration: Gen2`
3. When `--image-generation` is not specified, the NodePool defaults appropriately
4. The fix works with multiple availability zones

### Step 3: Verify Existing Tests Still Pass

Ensure that existing tests for both:
- `cmd/cluster/azure/create_test.go`
- `cmd/nodepool/azure/create_test.go`

continue to pass after the changes.

## Testing Strategy

### Manual Testing

1. Test Gen1 creation:
```bash
./hypershift create cluster azure --image-generation Gen1 [other-flags...]
oc get nodepool -n clusters -o yaml | grep imageGeneration
# Expected: imageGeneration: Gen1
```

2. Test Gen2 creation:
```bash
./hypershift create cluster azure --image-generation Gen2 [other-flags...]
oc get nodepool -n clusters -o yaml | grep imageGeneration
# Expected: imageGeneration: Gen2
```

3. Test default behavior (no flag):
```bash
./hypershift create cluster azure [other-flags...]
oc get nodepool -n clusters -o yaml | grep imageGeneration
# Expected: Should default to Gen2 or be nil (allowing controller defaulting)
```

4. Test with availability zones:
```bash
./hypershift create cluster azure --image-generation Gen1 --availability-zones 1,2,3 [other-flags...]
oc get nodepool -n clusters -o yaml | grep imageGeneration
# Expected: All three NodePools should have imageGeneration: Gen1
```

### Unit Testing

Create comprehensive unit tests covering:
- Single NodePool generation with Gen1
- Single NodePool generation with Gen2
- Multiple NodePool generation (with AZs) with Gen1
- Multiple NodePool generation (with AZs) with Gen2
- Default behavior when flag is not specified

## Impact Analysis

### Breaking Changes
None. This is a bug fix that makes the CLI behave as documented and expected.

### Affected Components
- `cmd/cluster/azure/create.go` - Main fix location
- `cmd/cluster/azure/create_test.go` - New tests

### Backward Compatibility
This fix is backward compatible. Users who weren't specifying `--image-generation` will continue to get Gen2 (current default behavior). Users who were specifying `--image-generation Gen1` but getting Gen2 will now get their requested Gen1 (fixing the bug).

## Additional Notes

- The fix preserves the existing pattern of allowing the nodepool controller to populate marketplace details from the release payload.
- The fix is minimal and focused only on transferring the user's `ImageGeneration` preference to the NodePool spec.
- The `cmd/nodepool/azure/create.go` already has the correct logic and doesn't need changes.
