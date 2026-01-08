# Azure

This section provides guides for deploying HyperShift hosted clusters on Microsoft Azure. There are two deployment models available, each with different management cluster platforms and authentication mechanisms.

## Deployment Models

### ARO HCP (Managed Azure)

ARO HCP (Azure Red Hat OpenShift Hosted Control Planes) uses an AKS (Azure Kubernetes Service) cluster as the management platform. This model uses Azure Managed Identities with certificate-based authentication, with credentials stored in Azure Key Vault.

**Guides:**

- [Create an Azure Hosted Cluster on AKS](create-azure-cluster-on-aks.md) - Step-by-step setup guide
- [Azure Hosted Cluster with Options](create-azure-cluster-with-options.md) - Advanced configuration options

### Self-Managed Azure

Self-managed Azure uses an OpenShift cluster (running on any platform - AWS, Azure, bare metal, etc.) as the management platform. This model uses Azure Workload Identity with OIDC federation for tokenless authentication.

!!! note "Developer Preview in OCP 4.21"

    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

**Guides:**

- [Self-Managed Azure Overview](self-managed-azure-index.md) - Architecture and deployment workflow
- [Azure Workload Identity Setup](azure-workload-identity-setup.md) - Set up managed identities and OIDC federation
- [Setup Azure Management Cluster](setup-management-cluster.md) - Install HyperShift operator
- [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md) - Deploy your first hosted cluster
- [Create Azure IAM Resources Separately](create-iam-separately.md) - Manage workload identities independently
- [Create Azure Infrastructure Separately](create-infra-separately.md) - Create infrastructure before cluster

## Comparison

| Aspect | ARO HCP | Self-Managed Azure |
|--------|---------|-------------------|
| **Management Cluster** | AKS | OpenShift (any platform) |
| **Control Plane Auth** | Certificate-based (Key Vault) | Workload Identity (OIDC) |
| **Data Plane Auth** | Federated Identity (OIDC) | Workload Identity (OIDC) |
| **Credential Storage** | Azure Key Vault | None (tokenless via OIDC) |
| **Identity Configuration** | Managed identities file + data plane identities file | Workload identities file |
| **Secrets Access** | Secrets Store CSI Driver | Projected ServiceAccount tokens |
| **Setup Complexity** | Higher (Key Vault, service principals, CSI driver) | Moderate (OIDC federation only) |
| **Automation Scripts** | Available in `contrib/managed-azure/` | Available in `contrib/self-managed-azure/` |

## Infrastructure Reference

For detailed information about the Azure infrastructure resources required for each deployment model, see:

- [ARO HCP Infrastructure](../../reference/infrastructure/azure-aro-hcp.md)
- [Self-Managed Azure Infrastructure](../../reference/infrastructure/azure-self-managed.md)

## Additional Resources

- [Azure Workload Identity Documentation](https://azure.github.io/azure-workload-identity/docs/)
- [Azure Managed Identities Documentation](https://learn.microsoft.com/en-us/azure/active-directory/managed-identities-azure-resources/overview)
- [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/)
