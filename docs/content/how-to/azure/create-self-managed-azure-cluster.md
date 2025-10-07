# Create a Self-Managed Azure HostedCluster

!!! note "Developer Preview in OCP 4.21"
    
    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

This document describes how to create a self-managed Azure HostedCluster using workload identities for authentication.

## Overview

Self-managed Azure HostedClusters use [Azure Workload Identity](https://azure.github.io/azure-workload-identity/docs/) for authentication.

## Prerequisites

Before creating a self-managed Azure HostedCluster, ensure you have:

- Azure CLI (`az`) installed and configured
- HyperShift CLI binary
- OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
- `jq` command-line JSON processor
- An Azure OpenShift management cluster with HyperShift operator and ExternalDNS installed
- External DNS configured for the management cluster
- Azure workload identities and OIDC issuer configured
- Appropriate Azure permissions (see [Permission Requirements](#permission-requirements))

!!! note "Setup Requirements"
    
    This guide assumes you have already completed the workload identity configuration and management cluster setup. Follow these guides in order:
    
    1. [Azure Workload Identity Setup](azure-workload-identity-setup.md) - Workload identities and OIDC issuer configuration
    2. [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) - ExternalDNS and HyperShift operator installation

### Permission Requirements

Your Azure service principal must have the following permissions:

- **Subscription Level**:
  - `Contributor` role
  - `User Access Administrator` role
- **Microsoft Graph API**:
  - `Application.ReadWrite.OwnedBy` permission (requires DPTP request in most cases)

## Creating the Self-Managed Azure HostedCluster

### Infrastructure Setup

Before creating the HostedCluster, set up the necessary Azure infrastructure:

!!! note "About PERSISTENT_RG_NAME"
    In Red Hat environments, a periodic Azure resource "reaper" deletes resources that are not properly tagged or not located in an approved resource group. We frequently use the `os4-common` resource group for shared, long-lived assets (for example, public DNS zones) to avoid accidental cleanup. If you are not in Red Hat infrastructure, set `PERSISTENT_RG_NAME` to any long-lived resource group in your subscription that will not be automatically reaped, or ensure your organization’s required tags/policies are applied. The name does not have to be `os4-common`—use whatever persistent resource group fits your environment.

```bash
# Set cluster configuration variables
PREFIX="your-prefix-sm"
RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:XYZ"
TAG="latest"

LOCATION="eastus"
MANAGED_RG_NAME="${PREFIX}-managed-rg"
VNET_RG_NAME="${PREFIX}-customer-vnet-rg"
NSG_RG_NAME="${PREFIX}-customer-nsg-rg"
VNET_NAME="${PREFIX}-customer-vnet"
VNET_SUBNET1="${PREFIX}-customer-subnet-1"
NSG="${PREFIX}-customer-nsg"
DNS_ZONE_NAME="your-subdomain.your-parent.dns.zone.com"
CLUSTER_NAMESPACE="clusters"
CLUSTER_NAME="${PREFIX}-hc"
AZURE_CREDS="/path/to/azure/credentials"
PULL_SECRET="/path/to/pull-secret.json"
HYPERSHIFT_BINARY_PATH="/path/to/hypershift/bin"
OIDC_ISSUER_URL="https://yourstorageaccount.blob.core.windows.net/yourstorageaccount"
SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="/path/to/serviceaccount-signer.private"
PERSISTENT_RG_NAME="os4-common"
PARENT_DNS_ZONE="your-parent.dns.zone.com"

# Clean up any previous instances (optional)
az group delete -n "${VNET_RG_NAME}" --yes --no-wait || true
az group delete -n "${NSG_RG_NAME}" --yes --no-wait || true

# Create managed resource group
az group create --name "${MANAGED_RG_NAME}" --location ${LOCATION}

# Create VNET & NSG resource groups
az group create --name "${VNET_RG_NAME}" --location ${LOCATION}
az group create --name "${NSG_RG_NAME}" --location ${LOCATION}

# Create network security group
az network nsg create \
    --resource-group "${NSG_RG_NAME}" \
    --name "${NSG}"

# Get NSG ID
GetNsgID=$(az network nsg list --query "[?name=='${NSG}'].id" -o tsv)

# Create VNet with subnet
az network vnet create \
    --name "${VNET_NAME}" \
    --resource-group "${VNET_RG_NAME}" \
    --address-prefix 10.0.0.0/16 \
    --subnet-name "${VNET_SUBNET1}" \
    --subnet-prefixes 10.0.0.0/24 \
    --nsg "${GetNsgID}"

# Get VNet and Subnet IDs
GetVnetID=$(az network vnet list --query "[?name=='${VNET_NAME}'].id" -o tsv)
GetSubnetID=$(az network vnet subnet show \
    --vnet-name "${VNET_NAME}" \
    --name "${VNET_SUBNET1}" \
    --resource-group "${VNET_RG_NAME}" \
    --query id --output tsv)
```

### Create the HostedCluster

!!! note "Federated Identity Prerequisites"
    
    Before creating the cluster, ensure that all federated identity credentials have been set up for your workload identities as described in the [Azure Workload Identity Setup](azure-workload-identity-setup.md) guide. The cluster creation will fail if these are not properly configured.

Create the HostedCluster with comprehensive configuration:

```bash
# Create the HostedCluster
${HYPERSHIFT_BINARY_PATH}/hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --namespace "$CLUSTER_NAMESPACE" \
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
    --control-plane-operator-image="quay.io/hypershift/hypershift:${TAG}" \
    --marketplace-publisher azureopenshift \
    --marketplace-offer aro4 \
    --marketplace-sku aro_419 \
    --marketplace-version 419.6.20250523 \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --assign-service-principal-roles \
    --workload-identities-file ./workload-identities.json \
    --diagnostics-storage-account-type Managed
```

!!! important "Key Configuration Options"
    
    - `--workload-identities-file`: References the workload identities configuration created in the setup guide
    - `--assign-service-principal-roles`: Automatically assigns required Azure roles to workload identities
    - `--sa-token-issuer-private-key-path`: Path to the private key for service account token signing
    - `--oidc-issuer-url`: URL of the OIDC issuer created in the workload identity setup
    - `--vnet-id`, `--subnet-id`, `--network-security-group-id`: Custom networking infrastructure
    - `--marketplace-*`: Azure Marketplace image information for ARO-based node images
    - `--dns-zone-rg-name`: Resource group containing the DNS zone (os4-common)
    - `--diagnostics-storage-account-type Managed`: Use Azure managed storage for diagnostics
    - `--control-plane-operator-image`: Custom HyperShift operator image (optional)

## Verification

Check the cluster status and access:

```bash
# Check HostedCluster status
oc get hostedcluster $CLUSTER_NAME -n clusters

# Wait for cluster to be available
oc wait --for=condition=Available hostedcluster/$CLUSTER_NAME -n clusters --timeout=30m

# Get kubeconfig and access the cluster
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
export KUBECONFIG=$CLUSTER_NAME-kubeconfig
oc get nodes
oc get clusterversion
```

## Cleanup

To delete the HostedCluster:

```bash
# Delete the HostedCluster
hypershift destroy cluster azure \
    --name $CLUSTER_NAME \
    --azure-creds $AZURE_CREDS \
    --resource-group-name $MANAGED_RG_NAME
```

!!! note "Resource Cleanup"
    
    The HyperShift destroy command will clean up the cluster resources. Workload identities and OIDC issuer created during setup can be reused for other clusters or cleaned up separately if no longer needed.

## Related Documentation

1. [Azure Workload Identity Setup](azure-workload-identity-setup.md) - Workload identities and OIDC issuer setup
2. [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) - DNS and HyperShift operator setup