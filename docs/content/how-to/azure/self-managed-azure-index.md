# Self-Managed Azure HyperShift Overview

!!! note "Developer Preview in OCP 4.21"

    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

## Introduction

Self-managed Azure HyperShift enables you to deploy and manage OpenShift hosted control planes on an OpenShift management cluster running in Azure. The key difference from managed Azure HyperShift deployments is the management cluster platform: self-managed uses an OpenShift cluster you provision and manage, while managed Azure uses Azure Kubernetes Service (AKS).

## Architecture Overview

Self-managed Azure HyperShift deployments consist of three key layers:

1. **Management Cluster**: An existing Azure OpenShift cluster that hosts the HyperShift operator and control planes for your hosted clusters
2. **Control Plane**: Kubernetes control plane components running as pods on the management cluster
3. **Data Plane**: Worker nodes running as Azure Virtual Machines in your Azure subscription

The architecture uses [Azure Workload Identity](https://azure.github.io/azure-workload-identity/docs/) for secure, credential-free authentication between OpenShift components and Azure services. This eliminates the need to manage long-lived service principal credentials and provides better security through federated identity credentials.

### Key Differences from Managed Azure

Unlike managed Azure HyperShift deployments, self-managed Azure:

- Uses an OpenShift cluster as the management platform instead of AKS
- Requires you to provision and manage the lifecycle of the management cluster

## Deployment Workflow

Setting up self-managed Azure HyperShift is a three-phase process that must be completed in order:

### Phase 1: Azure Workload Identity Setup

**Purpose**: Establish secure authentication infrastructure for OpenShift components to access Azure services.

This phase creates the foundational security infrastructure required for your hosted clusters:

- **Managed Identities**: Creates Azure managed identities for each OpenShift component (image registry, ingress, CSI drivers, cloud provider, network operator, etc.)
- **OIDC Issuer**: Configures an OIDC issuer in Azure Blob Storage for service account token validation
- **Federated Credentials**: Establishes trust relationships between Azure Entra ID and OpenShift service accounts

**Why This Matters**: Without these identities and federated credentials, your hosted cluster components cannot authenticate with Azure APIs to provision storage, manage load balancers, configure networking, or perform other essential cloud operations. Using workload identities instead of traditional service principals provides better security, automatic credential rotation, and follows Azure's modern authentication best practices.

**When to Complete**: This is a one-time setup that can be reused across multiple hosted clusters. Complete this before proceeding to Phase 2.

👉 **Guide**: [Azure Workload Identity Setup](azure-workload-identity-setup.md)

### Phase 2: Management Cluster Setup

**Purpose**: Prepare your Azure OpenShift management cluster to host and manage HyperShift control planes.

This phase configures the management cluster infrastructure:

- **DNS Zone Configuration**: Creates Azure DNS zones and delegates DNS records for hosted cluster API endpoints and application routes
- **External DNS**: Sets up a service principal and deploys ExternalDNS to automatically manage DNS records for hosted clusters
- **HyperShift Operator**: Installs the HyperShift operator that will create and manage hosted control planes

**Why This Matters**: The management cluster needs to automatically provision DNS records for each hosted cluster's API server and ingress endpoints. ExternalDNS watches for HostedCluster resources and creates the appropriate DNS records in Azure DNS. The HyperShift operator orchestrates the entire lifecycle of hosted control planes, from creation to upgrades to deletion.

**When to Complete**: After Phase 1 is complete, but before creating any hosted clusters.

👉 **Guide**: [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md)

### Phase 3: Create Hosted Clusters

**Purpose**: Deploy self-managed Azure hosted clusters with custom networking and configuration.

This phase creates your actual hosted OpenShift clusters:

- **Infrastructure Provisioning**: Creates resource groups, VNets, subnets, and network security groups
- **HostedCluster Creation**: Deploys the control plane on the management cluster and worker nodes in your Azure subscription
- **Workload Identity Integration**: Links the hosted cluster to the workload identities created in Phase 1

**Why This Matters**: This is where you deploy the actual OpenShift clusters that your applications will run on. Each hosted cluster gets its own control plane running on the management cluster and its own set of worker node VMs in Azure. The cluster uses the workload identities from Phase 1 to securely access Azure services without storing credentials.

**When to Complete**: After Phases 1 and 2 are fully configured and verified.

👉 **Guide**: [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md)

## Prerequisites Summary

Before beginning the deployment process, ensure you have:

- **Azure Resources**:

    - An existing Azure OpenShift management cluster
    - Azure subscription with appropriate permissions (Contributor + User Access Administrator)
    - A parent DNS zone in Azure DNS for delegating cluster DNS records

- **Tools and Access**:

    - Azure CLI (`az`) configured with your subscription
    - OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
    - HyperShift CLI binary
    - `jq` command-line JSON processor
    - Cloud Credential Operator (CCO) tool
    - Valid OpenShift pull secret

- **Permissions**:

    - Subscription-level Contributor and User Access Administrator roles
    - Microsoft Graph API permissions (Application.ReadWrite.OwnedBy) for creating service principals

## Resource Group Strategy

Self-managed Azure deployments use multiple resource groups with different lifecycles:

- **Persistent Resource Group** (e.g., `os4-common`): Long-lived resources shared across multiple clusters

    - Workload identities (managed identities)
    - OIDC issuer storage account
    - Azure DNS zones
    - External DNS service principal

- **Cluster-Specific Resource Groups**: Created and destroyed with each hosted cluster

    - Managed resource group for cluster infrastructure
    - VNet resource group (if using custom networking)
    - NSG resource group (if using custom networking)

!!! tip "Resource Reuse"

    By placing workload identities and OIDC issuer in a persistent resource group, you can:

    - Reuse the same identities across multiple hosted clusters
    - Avoid recreating federated credentials for each cluster
    - Reduce cluster creation time
    - Simplify cleanup when deleting individual clusters

## Security Considerations

Self-managed Azure HyperShift implements several security best practices:

1. **Workload Identity Federation**: Eliminates long-lived credentials by using OIDC-based authentication
2. **Least Privilege Access**: Each component gets its own managed identity with minimal required permissions
3. **Network Isolation**: Custom VNets and NSGs allow you to implement network segmentation and security policies
4. **Federated Credentials**: Trust relationships are scoped to specific service accounts, preventing unauthorized access

## Next Steps

Begin your self-managed Azure HyperShift deployment by following the guides in order:

1. [Azure Workload Identity Setup](azure-workload-identity-setup.md) - Set up managed identities and OIDC federation
2. [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) - Configure DNS and install HyperShift operator
3. [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md) - Deploy your first hosted cluster

## Additional Resources

- [Azure Workload Identity Documentation](https://azure.github.io/azure-workload-identity/docs/)
- [HyperShift Architecture Reference](../../reference/architecture/index.md)
- [Managed Azure Documentation](../azure/create-azure-cluster-on-aks.md) - For comparison with AKS-based deployments
