# Infrastructure Resources Needed Prior to Creating Azure HostedClusters on AKS

## Needed Networking Resources
TBD to be done in [HOSTEDCP-1890](https://issues.redhat.com/browse/HOSTEDCP-1890)

## Needed Identity Resources
### Management Cluster Side
There are several controllers on the hosted control plane (HCP) that will need user-assigned managed identities (aks 
MSI) to authenticate with Azure. In ARO HCP, these are expected to come from the customer; the client IDs related to 
these identities will be used in the HostedCluster (HC) custom resource (CR). A separate user-assigned managed 
identities will need to be created for each of these components:

* Azure cloud provider/cloud controller manager (CCM)
  * Needs `Contributor` over the resource group with the LoadBalancer
  * Needs `Network Contributor` over the resource group containing the Network Security Group
  * The MSI needs to be assigned to the VMSS/VMs in the AKS management cluster
* Cluster API Azure (CAPZ) 
  * Needs `Contributor` over the resource group with the LoadBalancer
  * Needs `Network Contributor` over the resource group containing the Network Security Group
  * Needs `Network Contributor` over the resource group containing the VNET
  * The MSI needs to be assigned to the VMSS/VMs in the AKS management cluster
* cluster-image-registry-operator 
  * Needs TBD
* cluster-ingress-operator 
    * Needs TBD
* cluster-network-operator
    * Needs TBD
* cluster-storage-operator for file and disk controllers
  * The same MSI used for Azure cloud provider/CCM is used for this operator since CSO uses the same configuration
* KMS 
  * Needs Contributor over the managed resource group
  * Needs `Key Vault Crypto User` to the key in the Azure Key Vault
* control-plane-operator (CPO)
  * Needs TBD

!!! note

    The roles currently listed above may change once [HOSTEDCP-1520](https://issues.redhat.com/browse/HOSTEDCP-1520) is 
    worked.