---
title: Create Azure infrastructure separately
---

# Create Azure infrastructure separately

The default behavior of the `hypershift create cluster azure` command is to create cloud infrastructure
along with the HostedCluster and apply it. It is possible to create the cloud infrastructure portion
separately so that the `hypershift create cluster azure` command can be used to create the cluster
with pre-existing infrastructure.

In order to do this, you need to:

1. Create the Azure infrastructure
2. Create the cluster using the infrastructure output

## Prerequisites

Before creating Azure infrastructure, ensure you have:

- An Azure credentials file with the following format:
    ```json
    {
      "subscriptionId": "your-subscription-id",
      "tenantId": "your-tenant-id",
      "clientId": "your-client-id",
      "clientSecret": "your-client-secret"
    }
    ```
- An existing public DNS zone in your Azure subscription for your base domain
- An OIDC issuer URL (for self-managed Azure with workload identity)

## Creating the Azure Infrastructure

Use the `hypershift create infra azure` command:

```bash
hypershift create infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --base-domain BASE_DOMAIN \
    --location LOCATION \
    --oidc-issuer-url OIDC_ISSUER_URL \
    --output-file OUTPUT_INFRA_FILE
```

where:

* `CLUSTER_NAME` is the name of the hosted cluster you intend to create.
* `INFRA_ID` is a unique name that will be used to identify your infrastructure. It is used
    to name and tag Azure resources. Typically this is the name of your cluster with a random
    suffix appended to it.
* `AZURE_CREDENTIALS_FILE` points to an Azure credentials file that has permission to create
    infrastructure resources such as VNets, subnets, load balancers, etc.
* `BASE_DOMAIN` is the base domain for your hosted cluster's ingress. It must correspond to
    an existing public DNS zone in your Azure subscription.
* `LOCATION` is the Azure region where you want to create the infrastructure (e.g., `eastus`, `westus2`).
* `OIDC_ISSUER_URL` is the URL of the OIDC identity provider used for workload identity federation.
* `OUTPUT_INFRA_FILE` is the file where details of the infrastructure will be stored in YAML format.

Running this command creates the following resources:

* 1 Resource Group (main cluster resource group)
* 1 Resource Group for VNet (if not using existing VNet)
* 1 Resource Group for NSG (if not using existing NSG)
* 1 Virtual Network with a default subnet
* 1 Network Security Group
* 1 Private DNS Zone
* 1 Private DNS Zone Virtual Network Link
* 1 Public IP Address for load balancer
* 1 Load Balancer with outbound rule for egress
* 7 Managed Identities with federated credentials (when using OIDC issuer URL)

All resources are tagged with the infrastructure ID for identification and cleanup.

## Using Existing Network Resources

You can use existing VNet, subnet, and network security group instead of creating new ones:

```bash
hypershift create infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --base-domain BASE_DOMAIN \
    --location LOCATION \
    --oidc-issuer-url OIDC_ISSUER_URL \
    --vnet-id /subscriptions/SUB_ID/resourceGroups/RG/providers/Microsoft.Network/virtualNetworks/VNET_NAME \
    --subnet-id /subscriptions/SUB_ID/resourceGroups/RG/providers/Microsoft.Network/virtualNetworks/VNET_NAME/subnets/SUBNET_NAME \
    --network-security-group-id /subscriptions/SUB_ID/resourceGroups/RG/providers/Microsoft.Network/networkSecurityGroups/NSG_NAME \
    --output-file OUTPUT_INFRA_FILE
```

## Enabling RBAC Role Assignment

To automatically assign required Azure RBAC roles to the workload identities:

```bash
hypershift create infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --base-domain BASE_DOMAIN \
    --location LOCATION \
    --oidc-issuer-url OIDC_ISSUER_URL \
    --assign-identity-roles \
    --dns-zone-rg-name DNS_ZONE_RG \
    --output-file OUTPUT_INFRA_FILE
```

where:

* `--assign-identity-roles` enables automatic RBAC role assignment for workload identities
* `DNS_ZONE_RG` is the name of the resource group containing your public DNS zone

## Create Workload Identities Separately

If you want to create workload identities separately before creating infrastructure, use the
dedicated IAM command:

```bash
hypershift create iam azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --location LOCATION \
    --resource-group-name EXISTING_RG \
    --oidc-issuer-url OIDC_ISSUER_URL \
    --output-file WORKLOAD_IDENTITIES_FILE
```

This creates the managed identities and federated credentials, saving them to a JSON file.
You can then use this file with `--workload-identities-file` during infrastructure or cluster creation.

For complete documentation, see [Create Azure IAM Resources Separately](create-iam-separately.md).

## Creating the Cluster

After the infrastructure is created, use the output file with the cluster creation command:

```bash
hypershift create cluster azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --location LOCATION \
    --base-domain BASE_DOMAIN \
    --pull-secret PULL_SECRET_FILE \
    --node-pool-replicas 3 \
    --infra-json OUTPUT_INFRA_FILE
```

The `--infra-json` flag points to the infrastructure output file generated by the
`create infra azure` command.

## Destroying the Infrastructure

To destroy the infrastructure that was created:

```bash
hypershift destroy infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE
```

### Preserving the Resource Group

If you want to preserve the main resource group but delete only cluster-specific resources:

```bash
hypershift destroy infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --preserve-resource-group
```

This is useful when you have other resources in the same resource group that should not be deleted.

## Command Reference

### Required Flags for `create infra azure`

| Flag | Description |
|------|-------------|
| `--name` | Name of the HostedCluster |
| `--infra-id` | Unique infrastructure identifier |
| `--azure-creds` | Path to Azure credentials JSON file |

### Identity Configuration (choose one)

| Option | Flags Required |
|--------|----------------|
| Self-managed with auto-generated identities | `--oidc-issuer-url` |
| Self-managed with pre-existing identities | `--workload-identities-file` |

To create workload identities separately, use `hypershift create iam azure` instead.
See [Create Azure IAM Resources Separately](create-iam-separately.md).

### Optional Flags

| Flag | Description |
|------|-------------|
| `--location` | Azure region (default: `eastus`) |
| `--cloud` | Azure cloud environment (default: `AzurePublicCloud`) |
| `--base-domain` | Base domain for cluster ingress |
| `--resource-group-name` | Custom resource group name |
| `--resource-group-tags` | Tags to apply to resource group |
| `--vnet-id` | Use existing VNet |
| `--subnet-id` | Use existing subnet |
| `--network-security-group-id` | Use existing NSG |
| `--assign-identity-roles` | Enable RBAC role assignment |
| `--dns-zone-rg-name` | DNS zone resource group (for role assignment) |
| `--assign-custom-hcp-roles` | Use custom HCP roles instead of Contributor |
| `--disable-cluster-capabilities` | Disable specific cluster capabilities |
| `--output-file` | Output file for infrastructure details |
