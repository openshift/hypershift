# Azure Infrastructure Commands

This directory contains the Azure infrastructure creation command for HyperShift HostedClusters.

## Overview

The `hypershift create infra azure` command creates the necessary Azure infrastructure resources for hosting HyperShift clusters. It supports two deployment models:

1. **ARO HCP (Azure Red Hat OpenShift Hosted Control Planes)** - A managed service that uses UserAssignedManagedIdentity authentication with pre-created managed identities
2. **Self-managed Azure** - Customer-managed clusters that use workload identity authentication. Can either create workload identities automatically or use pre-existing ones via file

The key difference is the authentication method: ARO HCP uses UserAssignedManagedIdentity while self-managed uses workload identity with federated credentials.

## Command Examples

### ARO HCP with Managed Identities

Use pre-created managed identities for ARO HCP deployment:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --base-domain example.com \
  --location eastus \
  --managed-identities-file /path/to/managed-identities.yaml \
  --data-plane-identities-file /path/to/data-plane-identities.yaml \
  --output-file infra-output.yaml
```

### Self-managed Azure with Auto-generated Workload Identities

Create infrastructure and automatically generate workload identities:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --base-domain example.com \
  --location eastus \
  --oidc-issuer-url https://my-oidc-issuer.com \
  --output-file infra-output.yaml
```

### Self-managed Azure with Pre-existing Workload Identities

Use pre-created workload identities from a JSON file:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --base-domain example.com \
  --location eastus \
  --workload-identities-file /path/to/workload-identities.json \
  --output-file infra-output.yaml
```

### Generate Workload Identities Only

Generate workload identities and save to JSON file without creating full infrastructure:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --location eastus \
  --resource-group-name my-existing-rg \
  --oidc-issuer-url https://my-oidc-issuer.com \
  --generate-managed-identities \
  --workload-identities-output-file workload-identities.json
```

### Using Existing Network Resources

Use existing VNet and security group:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --base-domain example.com \
  --location eastus \
  --oidc-issuer-url https://my-oidc-issuer.com \
  --vnet-id /subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/my-vnet \
  --subnet-id /subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet \
  --network-security-group-id /subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/my-nsg \
  --output-file infra-output.yaml
```

### Using Custom Resource Group

Specify a custom resource group and add tags:

```bash
hypershift create infra azure \
  --name my-cluster \
  --infra-id my-cluster-infra \
  --azure-creds /path/to/azure-creds.json \
  --base-domain example.com \
  --location eastus \
  --oidc-issuer-url https://my-oidc-issuer.com \
  --resource-group-name my-custom-rg \
  --resource-group-tags "environment=dev,team=platform" \
  --output-file infra-output.yaml
```

## Required Flags

- `--name`: Name of the HostedCluster
- `--infra-id`: Unique infrastructure identifier
- `--azure-creds`: Path to Azure credentials JSON file

## Identity Configuration Requirements

You must provide exactly one of the following identity configurations:

### For ARO HCP:
- `--managed-identities-file` and `--data-plane-identities-file` (both required)

### For Self-managed Azure:
- `--workload-identities-file` OR `--oidc-issuer-url`

### For Generate Mode:
- `--generate-managed-identities` with `--oidc-issuer-url`, `--workload-identities-output-file`, and `--resource-group-name`

## Flag Conflicts

The following flags are mutually exclusive:

- ARO HCP flags (`--managed-identities-file`, `--data-plane-identities-file`) cannot be used with self-managed Azure flags (`--workload-identities-file`)
- Within self-managed Azure: `--workload-identities-file` and `--oidc-issuer-url` are mutually exclusive
- Generate mode: `--generate-managed-identities` cannot be used with any identity file flags

## Output

### Normal Infrastructure Creation Mode

When creating full infrastructure, the command outputs information to the specified `--output-file` in YAML format, including:

- `baseDomain`: The base domain for the cluster
- `publicZoneID`: Public DNS zone ID
- `privateZoneID`: Private DNS zone ID
- `region`: Azure location/region
- `resourceGroupName`: Main resource group name
- `vnetID`: Virtual network ID
- `subnetID`: Subnet ID
- `bootImageID`: RHCOS boot image ID (if `--rhcos-image` provided)
- `infraID`: Infrastructure ID
- `securityGroupID`: Network security group ID
- `controlPlaneMIs`: Control plane managed identities (ARO HCP only)
- `dataPlaneIdentities`: Data plane identities (ARO HCP only)
- `workloadIdentities`: Workload identities (self-managed Azure only)

### Generate Mode (`--generate-managed-identities`)

When using `--generate-managed-identities`, the command:
- Creates only workload identities and federated credentials (no full infrastructure)
- Outputs workload identities to the specified `--workload-identities-output-file` in JSON format
- Does NOT use `--output-file` (generate mode is incompatible with full infrastructure creation)

## Azure Credentials File Format

The Azure credentials file should be a JSON file with the following structure:

```json
{
  "subscriptionId": "your-subscription-id",
  "tenantId": "your-tenant-id",
  "clientId": "your-client-id",
  "clientSecret": "your-client-secret"
}
```

## Code Architecture

This package follows a modular architecture with specialized managers for different Azure operations:

### File Structure

- `create.go` - Main CLI command and orchestration logic (~240 lines)
- `types.go` - Shared type definitions and data structures
- `resource_groups.go` - Azure resource group management operations
- `networking.go` - VNet, DNS, load balancer, and security group operations
- `images.go` - RHCOS image upload and management
- `rbac.go` - Role-based access control and Microsoft Graph API operations
- `identity.go` - Workload identity and federated credential management

### Manager Pattern

Each manager encapsulates domain-specific Azure operations:

- **ResourceGroupManager** - Creates and manages Azure resource groups
- **NetworkManager** - Handles virtual networks, DNS zones, load balancers, and security groups
- **ImageManager** - Manages RHCOS image upload and bootable image creation
- **RBACManager** - Handles role assignments and Microsoft Graph API interactions
- **IdentityManager** - Creates workload identities and federated credentials

This architecture provides:
- **Separation of concerns** - Each manager handles one specific domain
- **Better testability** - Managers can be unit tested independently
- **Improved maintainability** - Changes are isolated to specific areas
- **Cleaner orchestration** - Main function reads like a high-level workflow