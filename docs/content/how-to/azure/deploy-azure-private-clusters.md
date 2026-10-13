---
title: Deploy Azure private clusters
---

# Deploying Azure Private Clusters

By default, HyperShift guest clusters are publicly accessible through public DNS
and the management cluster's default router.

For private clusters on Azure, all communication between worker nodes and the hosted
control plane occurs over [Azure Private Link](https://learn.microsoft.com/en-us/azure/private-link/private-link-overview).
This guide walks through the process of configuring HyperShift for private cluster
support on Azure.

!!! note "Tech Preview in OCP 4.22"

    Private self-managed Azure HostedClusters are planned as a Tech Preview feature in OpenShift Container Platform 4.22.

## Before You Begin

This guide assumes you have completed the self-managed Azure setup described in the
[Self-Managed Azure Overview](self-managed-azure-index.md), including:

- An **OpenShift management cluster running on Azure** (not AKS). The private cluster
  workflow uses `oc get infrastructure cluster` to discover the management cluster's
  Azure resource group, VNet, and other platform details — these APIs are only available
  on OpenShift. For AKS-based management clusters, use managed Azure HyperShift (ARO HCP) instead.
- Azure Workload Identity and OIDC issuer configuration
- Management cluster with HyperShift operator installed (will be reinstalled with private support)
- Azure CLI (`az`), HyperShift CLI (`hypershift`), `oc`/`kubectl`, `jq`, and `yq`

## Overview

Private endpoint access uses Azure Private Link Service (PLS) to expose the hosted
control plane's internal load balancer to the guest cluster's VNet through a Private
Endpoint. Worker nodes resolve the API server hostname via Private DNS zones that
point to the Private Endpoint IP.

The workflow has five steps:

1. Prepare a NAT subnet in the management cluster's VNet
2. Install the HyperShift operator with private platform support
3. Create IAM resources
4. Create infrastructure
5. Create the private HostedCluster

## Step 1: Prepare the NAT Subnet

Azure Private Link Service requires a dedicated subnet for NAT IP allocation. This
subnet must be in the **management cluster's VNet** and must have
`privateLinkServiceNetworkPolicies` disabled.

!!! note "Region Requirement"

    The Private Link Service, NAT subnet, and management cluster's internal load balancer
    must all be in the **same Azure region**. The PLS is automatically created in the
    HostedCluster's configured location. Azure will reject PLS creation if the NAT subnet
    is in a different region.

First, identify the management cluster's VNet:

```bash
# Get the management cluster's infrastructure resource group
MGMT_INFRA_RG=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.azure.resourceGroupName}')

# Find the VNet in the infrastructure resource group
MGMT_VNET_NAME=$(az network vnet list --resource-group "${MGMT_INFRA_RG}" --query "[0].name" -o tsv)
MGMT_VNET_RG="${MGMT_INFRA_RG}"
```

Create the NAT subnet:

```bash
NAT_SUBNET_NAME="pls-nat-subnet"

# Check existing address space and subnets to choose a non-overlapping CIDR
az network vnet show \
    --resource-group "${MGMT_VNET_RG}" \
    --name "${MGMT_VNET_NAME}" \
    --query '{addressSpace: addressSpace.addressPrefixes, subnets: subnets[].{name: name, prefix: addressPrefix}}' \
    -o json

az network vnet subnet create \
    --resource-group "${MGMT_VNET_RG}" \
    --vnet-name "${MGMT_VNET_NAME}" \
    --name "${NAT_SUBNET_NAME}" \
    --address-prefixes 10.1.64.0/24 \
    --disable-private-link-service-network-policies true
```

!!! warning "Choose a Non-Overlapping CIDR"

    The `10.1.64.0/24` address prefix above is an **example only**. You must choose a
    CIDR range that does not overlap with any existing subnets in the management cluster's
    VNet. Check the VNet's address space and existing subnets before creating the NAT
    subnet. If the management cluster's VNet uses `10.0.0.0/16`, the NAT subnet must
    fall within that range (e.g., `10.0.64.0/24`) or you must first expand the VNet's
    address space.

Get the NAT subnet resource ID for later use:

```bash
NAT_SUBNET_ID=$(az network vnet subnet show \
    --resource-group "${MGMT_VNET_RG}" \
    --vnet-name "${MGMT_VNET_NAME}" \
    --name "${NAT_SUBNET_NAME}" \
    --query id -o tsv)
```

!!! important

    The NAT subnet **must** be in the management cluster's VNet, not the guest VNet.
    This is because the Private Link Service is created alongside the management
    cluster's internal load balancer.

!!! note

    The `--disable-private-link-service-network-policies true` flag is required.
    Without it, Azure will reject PLS creation on this subnet.

## Step 2: Install HyperShift Operator with Private Platform Support

To support private clusters, the HyperShift operator must be installed with
additional flags that configure Azure Private Link Service management.

You need credentials that allow the operator to manage PLS resources:

```bash
# Azure credentials file for PLS management (same format as standard Azure creds)
AZURE_PRIVATE_CREDS="/path/to/azure-private-credentials.json"

# Management cluster's infrastructure resource group
MGMT_INFRA_RG=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.azure.resourceGroupName}')
```

Install the operator with private platform support. The private-specific flags are
added **in addition to** the standard install flags (External DNS, pull secret, etc.):

```bash
hypershift install \
    --pull-secret ${PULL_SECRET} \
    --private-platform Azure \
    --azure-private-creds ${AZURE_PRIVATE_CREDS} \
    --azure-pls-resource-group ${MGMT_INFRA_RG} \
    # ... include your standard install flags (External DNS, etc.)
```

| Flag | Description |
|------|-------------|
| `--private-platform Azure` | Enables Azure Private Link Service management in the operator |
| `--azure-private-creds` | Path to Azure credentials file used for PLS operations |
| `--azure-pls-resource-group` | Resource group where PLS resources will be created (the management cluster's infrastructure RG) |

**Alternative authentication methods** (use one of these instead of `--azure-private-creds`):

| Flag | Description |
|------|-------------|
| `--azure-private-secret` | Name of an existing Kubernetes secret containing Azure credentials (use with `--azure-private-secret-key` to specify the key, default: `credentials`) |
| `--azure-pls-managed-identity-client-id` | Client ID of a managed identity for PLS operations via Azure Workload Identity federation (requires `--azure-pls-subscription-id`) |
| `--azure-pls-subscription-id` | Azure subscription ID for PLS operations (required with `--azure-pls-managed-identity-client-id`) |

!!! warning "Choose One Authentication Method"

    The three authentication methods (`--azure-private-creds`, `--azure-private-secret`,
    `--azure-pls-managed-identity-client-id`) are **mutually exclusive**. Use exactly one.

!!! important "Re-install Required for Private Support"

    If you already installed HyperShift without `--private-platform Azure`, you **must**
    re-run `hypershift install` with the private platform flags before creating any
    private clusters. The operator will not watch `AzurePrivateLinkService` CRs until
    configured with private platform support. You can safely re-run `hypershift install`
    to update the existing installation.

## Step 3: Create IAM Resources

Create workload identities for the cluster. The `create iam azure` command always creates
a Control Plane Operator identity, which is used by private clusters to manage Private
Endpoints and Private DNS zones in the guest subscription.

```bash
PREFIX="your-prefix"
CLUSTER_NAME="${PREFIX}-hc"
RESOURCE_GROUP_NAME="${CLUSTER_NAME}-${PREFIX}"
LOCATION="eastus"
AZURE_CREDS="/path/to/azure-credentials.json"
OIDC_ISSUER_URL="https://yourstorageaccount.blob.core.windows.net/yourstorageaccount"
WORKLOAD_IDENTITIES_FILE="./workload-identities.json"

hypershift create iam azure \
    --name "${CLUSTER_NAME}" \
    --infra-id "${PREFIX}" \
    --azure-creds "${AZURE_CREDS}" \
    --location "${LOCATION}" \
    --resource-group-name "${RESOURCE_GROUP_NAME}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --output-file "${WORKLOAD_IDENTITIES_FILE}"
```

The command creates 8 workload identities, including the Control Plane Operator identity:

| Identity | Operator | Azure Role | Scopes |
|----------|----------|------------|--------|
| **Control Plane Operator** | CPO | Contributor (default) or Custom HCP Role | Managed RG, NSG RG, VNet RG |

This identity allows the CPO to create and manage Private Endpoints, Private DNS zones,
VNet links, and DNS A records in the guest subscription.

!!! note

    The CPO identity is assigned the **Contributor** role by default. When using
    `--assign-custom-hcp-roles`, a more restrictive custom role is used instead.

## Step 4: Create Infrastructure

Create the Azure infrastructure. The `create infra azure` command creates the same
resources regardless of endpoint access topology:

```bash
DNS_ZONE_RG_NAME="os4-common"
PARENT_DNS_ZONE="your-base.domain.com"
INFRA_OUTPUT_FILE="${PREFIX}-infra-output.json"

hypershift create infra azure \
    --azure-creds "${AZURE_CREDS}" \
    --infra-id "${PREFIX}" \
    --name "${CLUSTER_NAME}" \
    --location "${LOCATION}" \
    --base-domain "${PARENT_DNS_ZONE}" \
    --dns-zone-rg-name "${DNS_ZONE_RG_NAME}" \
    --workload-identities-file "${WORKLOAD_IDENTITIES_FILE}" \
    --assign-identity-roles \
    --output-file "${INFRA_OUTPUT_FILE}"
```

## Step 5: Create the Private HostedCluster

Read the infrastructure output to get the resource IDs created in Step 4:

```bash
MANAGED_RG_NAME=$(yq -r -p yaml '.resourceGroupName' "${INFRA_OUTPUT_FILE}")
VNET_ID=$(yq -r -p yaml '.vnetID' "${INFRA_OUTPUT_FILE}")
SUBNET_ID=$(yq -r -p yaml '.subnetID' "${INFRA_OUTPUT_FILE}")
NSG_ID=$(yq -r -p yaml '.securityGroupID' "${INFRA_OUTPUT_FILE}")
```

Create the private HostedCluster:

```bash
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --namespace "clusters" \
    --azure-creds ${AZURE_CREDS} \
    --location ${LOCATION} \
    --node-pool-replicas 2 \
    --base-domain ${PARENT_DNS_ZONE} \
    --pull-secret ${PULL_SECRET} \
    --generate-ssh \
    --release-image ${RELEASE_IMAGE} \
    --resource-group-name "${MANAGED_RG_NAME}" \
    --vnet-id "${VNET_ID}" \
    --subnet-id "${SUBNET_ID}" \
    --network-security-group-id "${NSG_ID}" \
    --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --dns-zone-rg-name ${DNS_ZONE_RG_NAME} \
    --assign-service-principal-roles \
    --workload-identities-file ${WORKLOAD_IDENTITIES_FILE} \
    --diagnostics-storage-account-type Managed \
    --external-dns-domain ${DNS_ZONE_NAME} \
    --endpoint-access Private \
    --endpoint-access-private-nat-subnet-id "${NAT_SUBNET_ID}"
```

!!! note

    The `--endpoint-access` flag accepts three values:

    - `Public` (default): API server accessible via public endpoint only
    - `PublicAndPrivate`: API server accessible via both public and private endpoints
    - `Private`: API server accessible only via Private Link (private endpoint)

!!! warning "Endpoint Access Type is Immutable"

    You **cannot** change a cluster between `Public` and non-Public (`Private` or
    `PublicAndPrivate`) after creation. Transitions between `PublicAndPrivate` and
    `Private` are allowed, but switching from `Public` to `Private` (or vice versa)
    requires creating a new cluster.

!!! tip "Additional Allowed Subscriptions"

    If you need to allow Private Endpoint connections from Azure subscriptions other
    than the guest cluster's own subscription, use the
    `--endpoint-access-private-additional-allowed-subscriptions` flag:

    ```bash
    --endpoint-access-private-additional-allowed-subscriptions "sub-id-1,sub-id-2"
    ```

## Verify Private Connectivity

After creating the cluster, monitor the Private Link Service setup progress:

```bash
# Check AzurePrivateLinkService resources
oc get azureprivatelinkservices -n clusters-${CLUSTER_NAME}

# Check detailed status and conditions
oc get azureprivatelinkservices -n clusters-${CLUSTER_NAME} -o yaml
```

The conditions should progress through these stages:

| Condition | Description |
|-----------|-------------|
| `AzureInternalLoadBalancerAvailable` | Internal load balancer has a frontend IP |
| `AzurePLSCreated` | Private Link Service created in management cluster |
| `AzurePrivateEndpointAvailable` | Private Endpoint created in guest VNet |
| `AzurePrivateDNSAvailable` | Private DNS zones and A records created |
| `AzurePrivateLinkServiceAvailable` | All components ready, private connectivity available |

Check overall cluster status:

```bash
oc get hostedcluster ${CLUSTER_NAME} -n clusters
oc wait --for=condition=Available hostedcluster/${CLUSTER_NAME} -n clusters --timeout=30m
```

## Access a Private HostedCluster

### Generate a Kubeconfig

```bash
hypershift create kubeconfig --name ${CLUSTER_NAME} --port-forward > ${CLUSTER_NAME}-kubeconfig
```

### Port-Forward Method

If you have access to the management cluster, you can port-forward to the API server:

```bash
# Port-forward the kube-apiserver service
kubectl port-forward svc/kube-apiserver -n clusters-${CLUSTER_NAME} 6443:6443 &

# Use the kubeconfig (it will connect via localhost:6443)
KUBECONFIG=${CLUSTER_NAME}-kubeconfig oc get nodes
```

### VNet-Peered Access

If you have a VM in a VNet that is peered with the guest VNet, you can access the
API server, but you must first link the Private DNS zones to the peered VNet:

```bash
# Link the hypershift.local Private DNS zone to your peered VNet
PEERED_VNET_ID="/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Network/virtualNetworks/<vnet>"

az network private-dns link vnet create \
    --resource-group "${MANAGED_RG_NAME}" \
    --zone-name "${CLUSTER_NAME}.hypershift.local" \
    --name "peered-vnet-link" \
    --virtual-network "${PEERED_VNET_ID}" \
    --registration-enabled false

# If you also need base domain resolution (for OAuth/console):
az network private-dns link vnet create \
    --resource-group "${MANAGED_RG_NAME}" \
    --zone-name "${PARENT_DNS_ZONE}" \
    --name "peered-vnet-basedomain-link" \
    --virtual-network "${PEERED_VNET_ID}" \
    --registration-enabled false

# Then access the cluster
KUBECONFIG=${CLUSTER_NAME}-kubeconfig oc get nodes
```

!!! warning "Private DNS Zones Are Only Linked to the Guest VNet"

    The CPO only links Private DNS zones to the **guest cluster's VNet**. If you want
    to resolve the API server hostname from a peered VNet, you must manually link the
    Private DNS zones to that VNet as shown above. Without this step, DNS resolution
    will fail from the peered VNet.

## Cleanup

To delete a private HostedCluster:

```bash
hypershift destroy cluster azure \
    --name ${CLUSTER_NAME} \
    --azure-creds ${AZURE_CREDS} \
    --resource-group-name ${MANAGED_RG_NAME} \
    --dns-zone-rg-name ${DNS_ZONE_RG_NAME}
```

The deletion process automatically cleans up Private Link resources in the correct order:

1. The control plane operator removes the Private Endpoint, Private DNS zones, VNet links, and A records
2. The HyperShift operator removes the Private Link Service

!!! note "Cleanup Order"

    The dual-finalizer pattern ensures resources are deleted in the correct dependency
    order. The CPO finalizer runs first (removing guest-side resources), then the HO
    finalizer runs (removing management-side resources).

## Gotchas and Troubleshooting

### Management Cluster Requirements

- The management cluster **must be an OpenShift cluster running on Azure**, not AKS.
  Commands like `oc get infrastructure cluster` are used to discover the management
  cluster's Azure resource group and VNet, and these only work on OpenShift.
  For AKS-based management clusters, use managed Azure HyperShift (ARO HCP) instead.

- The HyperShift operator **must be installed with `--private-platform Azure`** before
  creating any private clusters. If you followed the
  [management cluster setup guide](setup-management-cluster.md) without private flags,
  re-run `hypershift install` with the additional private platform flags.

### NAT Subnet

- The NAT subnet CIDR (`--address-prefixes`) must fall within the management cluster's
  VNet address space. If the VNet uses `10.0.0.0/16`, a NAT subnet of `10.1.64.0/24`
  will fail unless you first expand the VNet address space.

- The `--disable-private-link-service-network-policies true` flag is **required** on
  the NAT subnet. If omitted, Azure will reject PLS creation with an error about
  network policies. This error is not always obvious — if PLS creation fails, check
  this setting first:

    ```bash
    az network vnet subnet show \
        --resource-group "${MGMT_VNET_RG}" \
        --vnet-name "${MGMT_VNET_NAME}" \
        --name "${NAT_SUBNET_NAME}" \
        --query privateLinkServiceNetworkPolicies
    ```

    The value must be `"Disabled"`.

### Endpoint Access Immutability

- You **cannot** change a cluster from `Public` to `Private` (or `Private` to `Public`)
  after creation. The API validation rejects this transition. You can only switch between
  `PublicAndPrivate` and `Private`.

- If you need to change a public cluster to private, you must create a new cluster with
  `--endpoint-access Private` from the start.

### Cross-Subscription Scenarios

- If the management cluster and guest cluster are in **different Azure subscriptions**,
  you must include the guest subscription in the PLS auto-approval list using
  `--endpoint-access-private-additional-allowed-subscriptions` with the guest's
  subscription ID.

- The CPO workload identity must also have permissions (Contributor or custom role) in
  the guest subscription's resource groups to create Private Endpoints and DNS resources.

### Private DNS Resolution

- Private DNS zones are only linked to the **guest cluster's VNet**. If you need to
  access the API server from a peered VNet, you must manually link the Private DNS
  zones to that VNet (see [VNet-Peered Access](#vnet-peered-access) above).

- Two Private DNS zones are created:
    1. `<clusterName>.hypershift.local` — synthetic internal zone with `api` and `*.apps` records
    2. `<baseDomain>` — base domain zone with `api-<clusterName>` and `oauth-<clusterName>` records

### Condition Debugging

If the cluster gets stuck, check the `AzurePrivateLinkService` CR conditions:

```bash
oc get azureprivatelinkservices -n clusters-${CLUSTER_NAME} -o jsonpath='{.items[0].status.conditions}' | jq .
```

| Stuck Condition | Likely Cause |
|-----------------|-------------|
| `AzureInternalLoadBalancerAvailable` = False | The `private-router` Service hasn't received an ILB IP yet. Check the Service status and Azure networking. |
| `AzurePLSCreated` = False | PLS creation failed. Check NAT subnet policies, credentials, and the HO operator logs. |
| `AzurePrivateEndpointAvailable` = False | PE creation failed or connection not approved. Check the PLS auto-approval list and CPO logs. |
| `AzurePrivateDNSAvailable` = False | DNS zone or record creation failed. Check CPO identity permissions in the guest subscription. |

## Related Documentation

- [Azure Private Link Architecture](../../reference/architecture/azure/privatelink.md) - Detailed architecture reference
- [Self-Managed Azure Overview](self-managed-azure-index.md) - Complete self-managed Azure guide
- [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md) - Standard (public) cluster creation
- [Azure Self-Managed Infrastructure Reference](../../reference/infrastructure/azure-self-managed.md) - Infrastructure details
