# OCPBUGS-33372: HyperShift Has Usages of Azure Clients that Aren't Cloud Agnostic

## Problem Statement

The HyperShift CLI creates Azure SDK clients without passing cloud options, which causes failures when trying to create hosted clusters in non-public Azure cloud environments (e.g., Azure Government Cloud, Azure China Cloud).

Currently, all Azure client instantiations pass `nil` as the third parameter (client options), which defaults to Azure Public Cloud. This prevents the CLI from working in other Azure cloud environments.

## Context

- PR #3804 introduced a `Cloud` parameter on the `AzurePlatformSpec` that can be used to specify which Azure cloud environment to use
- The `Cloud` field is an enum with values: `AzurePublicCloud`, `AzureUSGovernmentCloud`, `AzureChinaCloud`, `AzureGermanCloud`, `AzureStackCloud`
- The Azure SDK's `arm.ClientOptions` struct has a `Cloud` field that should be set based on the HostedCluster's cloud configuration
- Current code in control-plane-operator already shows the pattern: `azcore.ClientOptions{Cloud: cloud.AzurePublic}` (hardcoded)

## Affected Files

Based on codebase analysis, the following files have Azure client instantiations with `nil` options:

### cmd/infra/azure/
1. `cmd/infra/azure/networking.go` - Lines 33, 56, 101, 151, 191, 239
   - NewZonesClient
   - NewSecurityGroupsClient
   - NewVirtualNetworksClient
   - NewPrivateZonesClient
   - NewVirtualNetworkLinksClient
   - NewPublicIPAddressesClient

2. `cmd/infra/azure/resource_groups.go` - Line 35
   - NewResourceGroupsClient

3. `cmd/infra/azure/destroy.go` - Lines 78, 84
   - NewResourceGroupsClient
   - NewClient (resources)

### cmd/cluster/azure/
4. `cmd/cluster/azure/destroy.go` - Line 89
   - NewResourceGroupsClient

### support/azureutil/
5. `support/azureutil/azureutil.go` - Lines 130, 170, 185
   - NewVirtualNetworksClient
   - NewSecurityGroupsClient
   - NewResourceGroupsClient

## Solution Design

### Key Insight

The `Cloud` field already exists in the API at `HostedCluster.Spec.Platform.Azure.Cloud` with:
- Default value: `AzurePublicCloud`
- Optional field (can be omitted)
- Enum validation for valid cloud names

This means different approaches for different command types:

### Approach by Command Type

#### 1. Infrastructure Commands (cmd/infra/azure/)

**Context**: These run independently, often before the HostedCluster exists

**Solution**:
- Add `--cloud` CLI flag with default value `AzurePublicCloud` (matching API default)
- Store in options struct
- Use when creating Azure clients

**Files affected**:
- `cmd/infra/azure/create.go`
- `cmd/infra/azure/destroy.go`
- `cmd/infra/azure/networking.go`
- `cmd/infra/azure/resource_groups.go`

#### 2. Cluster Commands (cmd/cluster/azure/)

**Context**: These have access to the HostedCluster resource

**Solution**:
- Read cloud value from `hostedCluster.Spec.Platform.Azure.Cloud`
- No CLI flag needed - use the value from the spec
- If HostedCluster doesn't exist yet, use CLI flag as fallback

**Files affected**:
- `cmd/cluster/azure/destroy.go`

#### 3. Utility Functions (support/azureutil/)

**Context**: Called from various places with different contexts

**Solution**:
- Add cloud parameter to function signatures where clients are created
- Callers provide the cloud value from their context (CLI flag or HostedCluster spec)
- Default to `AzurePublicCloud` if not provided for backward compatibility

**Files affected**:
- `support/azureutil/azureutil.go`

### Helper Function

Create a helper function to convert cloud string to `cloud.Configuration`:

```go
// GetAzureCloudConfiguration converts a cloud name string to the Azure SDK cloud.Configuration
func GetAzureCloudConfiguration(cloudName string) (cloud.Configuration, error) {
	switch cloudName {
	case "AzurePublicCloud", "":
		return cloud.AzurePublic, nil
	case "AzureUSGovernmentCloud":
		return cloud.AzureGovernment, nil
	case "AzureChinaCloud":
		return cloud.AzureChina, nil
	case "AzureGermanCloud":
		return cloud.AzureGermany, nil
	default:
		return cloud.Configuration{}, fmt.Errorf("unknown Azure cloud: %s", cloudName)
	}
}
```

### Client Instantiation Pattern

All client instantiations should follow this pattern:

```go
cloudConfig, err := GetAzureCloudConfiguration(cloudName)
if err != nil {
	return err
}
clientOptions := &arm.ClientOptions{Cloud: cloudConfig}

// Example:
zonesClient, err := armdns.NewZonesClient(subscriptionID, creds, clientOptions)
```

## Implementation Steps

### Phase 1: Helper Function (Foundation)

1. **Create helper in support/azureutil/azureutil.go**
   - Add `GetAzureCloudConfiguration(cloudName string) (cloud.Configuration, error)`
   - Add comprehensive unit tests for all cloud types
   - Test error handling for invalid cloud names

### Phase 2: Infrastructure Commands (No HostedCluster Access)

2. **Update cmd/infra/azure/create.go**
   - Add `Cloud string` field to `CreateInfraOptions` struct
   - Add `--cloud` flag with default "AzurePublicCloud"
   - Pass cloud string to NetworkManager and ResourceGroupManager constructors
   - Update validation to check cloud value is valid

3. **Update cmd/infra/azure/networking.go**
   - Add `cloud string` field to `NetworkManager` struct
   - Update `NewNetworkManager` to accept cloud parameter
   - Update 6 client instantiations:
     - Line 33: NewZonesClient
     - Line 56: NewSecurityGroupsClient
     - Line 101: NewVirtualNetworksClient
     - Line 151: NewPrivateZonesClient
     - Line 191: NewVirtualNetworkLinksClient
     - Line 239: NewPublicIPAddressesClient

4. **Update cmd/infra/azure/resource_groups.go**
   - Add `cloud string` field to `ResourceGroupManager` struct
   - Update `NewResourceGroupManager` to accept cloud parameter
   - Update 1 client instantiation (Line 35: NewResourceGroupsClient)

5. **Update cmd/infra/azure/destroy.go**
   - Add `Cloud string` field to `DestroyInfraOptions` struct
   - Add `--cloud` flag with default "AzurePublicCloud"
   - Update 2 client instantiations:
     - Line 78: NewResourceGroupsClient
     - Line 84: NewClient

### Phase 3: Cluster Commands (HostedCluster Available)

6. **Update cmd/cluster/azure/destroy.go**
   - Extract cloud from `hostedCluster.Spec.Platform.Azure.Cloud` if HostedCluster exists
   - Fall back to CLI flag if HostedCluster doesn't exist
   - Update 1 client instantiation (Line 89: NewResourceGroupsClient)

### Phase 4: Utility Functions (Mixed Context)

7. **Update support/azureutil/azureutil.go**
   - Add `cloud string` parameter to functions that create clients:
     - `GetVnetInfoFromVnetID` (calls getFullVnetInfo)
     - `getFullVnetInfo` (Line 130: NewVirtualNetworksClient)
     - `GetNetworkSecurityGroupInfo` (Line 170: NewSecurityGroupsClient)
     - `GetResourceGroupInfo` (Line 185: NewResourceGroupsClient)
   - Default to "AzurePublicCloud" if not provided
   - Update all callers to pass cloud value

### Phase 5: Testing

8. **Add comprehensive tests**
   - Unit tests for `GetAzureCloudConfiguration`
   - Update existing tests to verify cloud configuration
   - Add test cases for each cloud environment

## Testing Strategy

1. **Unit Tests**
   - Test `GetAzureCloudConfiguration` with all valid cloud names
   - Test error handling for invalid cloud names

2. **Manual Testing**
   - Test CLI with `--cloud AzurePublicCloud` (default)
   - Test CLI with `--cloud AzureUSGovernmentCloud`
   - Verify clients are created with correct cloud configuration

## Backwards Compatibility

- Default value of "AzurePublicCloud" maintains current behavior
- Existing code that doesn't specify cloud will continue to work
- New `--cloud` flag is optional

## Rollout Plan

1. Implement changes in cmd/infra/azure/ first
2. Implement changes in cmd/cluster/azure/
3. Implement changes in support/azureutil/
4. Add comprehensive tests
5. Update documentation

## Success Criteria

- All Azure client instantiations pass proper cloud configuration
- CLI works in Azure Government Cloud
- CLI works in Azure China Cloud
- No regressions in Azure Public Cloud
- All tests pass
