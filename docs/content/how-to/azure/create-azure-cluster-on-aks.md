# Create an Azure Hosted Cluster on AKS
## General
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup. 
Azure HostedClusters on AKS are supported from OCP 4.19.0+.

This guide provides both automated script-based setup and manual step-by-step instructions. The automated scripts are located in the [/contrib/managed-azure folder in the HyperShift repo](https://github.com/openshift/hypershift/tree/main/contrib/managed-azure) and can significantly simplify the setup process.

## Prerequisites
- Azure CLI
- jq
- oc (OpenShift CLI)
- kubectl
- ccoctl tool
  - The ccoctl tool provides various commands to assist with the creating and maintenance of cloud credentials from outside
    a cluster. More information on the tool can be found [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md).

## Automated Setup (Recommended)

For the quickest setup, you can use the automated scripts. First, create your configuration:

### Quick Start with Automation Scripts

1. **Create your user configuration file**:
   ```sh
   cat <<EOF > user-vars.sh
   # User variables.
   export PREFIX="YOUR-management"
   export PULL_SECRET="/path/to/pull-secret.txt"
   export HYPERSHIFT_BINARY_PATH="/path/to/hypershift/bin/"
   export HYPERSHIFT_IMAGE="quay.io/hypershift/hypershift-operator:latest"
   export RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:4.20.0-ec.3-multi"
   export LOCATION="eastus"
   export AZURE_CREDS="/path/to/azure-creds.json"
   # Azure storage account names must be between 3 and 24 characters in length and may contain numbers and lowercase letters only.
   export OIDC_ISSUER_NAME="YOURmanagement"
   EOF
   ```

2. **Create Azure credentials file** (see Manual Setup Step 2 below for details)

3. **Run the complete automated setup** (authentication is automatic):
   
   For your **first cluster** (includes one-time resource setup):
   ```sh
   ../contrib/managed-azure/setup_all.sh --first-time
   ```
   
   For **additional clusters** (reuses existing resources):
   ```sh
   ../contrib/managed-azure/setup_all.sh
   ```
   
   View the script: [setup_all.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_all.sh)

!!! note "Automatic Authentication"
    
    The setup script automatically logs you into Azure if you're not already authenticated. No separate login step is required!

!!! warning "Important: One-Time Setup Components"
    
    Three scripts create resources that should be **reused across multiple clusters** to avoid quota issues: setup_MIv3_kv.sh (service principals and Key Vault), setup_oidc_provider.sh (OIDC issuer), and setup_dataplane_identities.sh (data plane identities). Use the `--first-time` flag only for your first cluster setup. For subsequent clusters, run the script without this flag to skip the one-time setup and reuse existing resources.

!!! tip
    The `--first-time` flag automatically handles the one-time setup resources. If you need to run individual scripts for troubleshooting, you can execute them directly from the contrib/managed-azure folder. For manual Azure authentication, a `login.sh` script is also available.

The automated setup runs these scripts in sequence:

- [setup_MIv3_kv.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_MIv3_kv.sh) - Sets up control plane identities and Key Vault
- [setup_aks_cluster.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_aks_cluster.sh) - Creates the AKS management cluster  
- [setup_external_dns.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_external_dns.sh) - Configures DNS zones and external DNS
- [setup_install_ho_on_aks.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_install_ho_on_aks.sh) - Installs the HyperShift operator
- [setup_oidc_provider.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_oidc_provider.sh) - Sets up OIDC issuer for workload identity
- [setup_dataplane_identities.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_dataplane_identities.sh) - Creates data plane managed identities
- [create_basic_hosted_cluster.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/create_basic_hosted_cluster.sh) - Creates the hosted cluster

## Manual Setup (Step-by-Step)

If you prefer to understand each step or need to customize the process, follow these manual steps:

## Configuration Notes

!!! tip

    For most users, the automated scripts handle all variable configuration automatically. The variables you need to set are defined in your `user-vars.sh` file as shown in the Quick Start section above.

    The `PARENT_DNS_ZONE` value may be different for different teams. Check the `os4-common` resource group associated with your subscription for pre-existing DNS zones. If there are multiple DNS zones, the parent DNS zone is typically the common suffix among them (e.g., `hypershift.azure.devcluster.openshift.com`).

## Manual Steps

### 1. Retrieve User Account Details
**Goal**: Get your Azure subscription and tenant information for authentication setup.

```sh
ACCOUNT_DETAILS=$(az account show --query '{subscriptionId: id, tenantId: tenantId}' -o json)
SUBSCRIPTION_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.subscriptionId')
TENANT_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.tenantId')
```

### 2. Create Service Principal for Authentication
**Goal**: Create a service principal that will be used by the HyperShift CLI to create cluster infrastructure.

```sh
SP_DETAILS=$(az ad sp create-for-rbac --name "$PERSONAL_SP_NAME" --role Contributor --scopes "/subscriptions/$SUBSCRIPTION_ID" -o json)
CLIENT_ID=$(echo "$SP_DETAILS" | jq -r '.appId')
CLIENT_SECRET=$(echo "$SP_DETAILS" | jq -r '.password')

cat <<EOF > $SP_AKS_CREDS
{
  "subscriptionId": "$SUBSCRIPTION_ID",
  "tenantId": "$TENANT_ID",
  "clientId": "$CLIENT_ID",
  "clientSecret": "$CLIENT_SECRET"
}
EOF
```
!!! warning
      
    In order for your Hypershift cluster to create properly, the Microsoft Graph `Application.ReadWrite.OwnedBy` 
    permission must be added to your Service Principal and it also must be assigned to User Access Administrator at the 
    subscription level. 

    In most cases, you'll need to submit a DPTP request to have this done.

### 3. Control Plane Identity and Key Vault Setup
**Goal**: Set up managed identities, key vault, and service principals required for the control plane components. This includes creating certificate-based authentication for various OpenShift services and storing credentials securely.

!!! warning "One-Time Setup Only (Steps 3-5)"
    
    Steps 3-5 create resources that should be **reused across multiple clusters** to avoid Azure quota limits: service principals and Key Vault (setup_MIv3_kv.sh), OIDC issuer (setup_oidc_provider.sh), and data plane identities (setup_dataplane_identities.sh). Only run these steps once per environment. For subsequent clusters, use `setup_all.sh` without the `--first-time` flag to skip these one-time setup steps.

**Automated Script**: [setup_MIv3_kv.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_MIv3_kv.sh)

This script handles:

- Creating managed identities for AKS cluster components
- Setting up Azure Key Vault with proper RBAC authorization
- Creating service principals for control plane services (cloud provider, disk, file, image registry, etc.)
- Generating and storing certificate JSON in Key Vault
- Creating a JSON credential file for the service principals

### 4. OIDC Provider Setup
**Goal**: Create RSA keys and OIDC issuer for workload identity authentication.

**Automated Script**: [setup_oidc_provider.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_oidc_provider.sh)

This script handles:

- Creating RSA key pairs for service account token signing
- Setting up the OIDC issuer URL in Azure storage
- Configuring the issuer for workload identity federation

### 5. Data Plane Identity Configuration
**Goal**: Create managed identities for data plane components and configure federated identity credentials for workload identity.

**Automated Script**: [setup_dataplane_identities.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_dataplane_identities.sh)

This script handles:

- Creating managed identities for Azure Disk CSI, Azure File CSI, and Image Registry
- Setting up federated identity credentials linking these identities to specific service accounts
- Generating the data plane identities configuration file

### 6. AKS Management Cluster Creation
**Goal**: Create and configure the AKS cluster that will host the HyperShift operator and manage hosted clusters.

**Automated Script**: [setup_aks_cluster.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_aks_cluster.sh)

This script handles:

- Creating a resource group for the AKS cluster
- Creating managed identities for AKS cluster and kubelet
- Creating the AKS cluster with required features (Key Vault secrets provider, FIPS, autoscaling, etc.)
- Configuring kubeconfig access
- Setting up role assignments for the Key Vault secrets provider

### 7. DNS Configuration Setup
**Goal**: Create DNS zones and configure external DNS for cluster ingress and API access.

**Automated Script**: [setup_external_dns.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_external_dns.sh)

This script handles:

- Creating a DNS zone for your cluster
- Configuring name server delegation to the parent DNS zone
- Creating a service principal for external DNS management
- Setting up proper DNS permissions and role assignments

### 8. HyperShift Operator Installation
**Goal**: Install the HyperShift operator on the AKS management cluster with proper external DNS and Azure integration.

**Automated Script**: [setup_install_ho_on_aks.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/setup_install_ho_on_aks.sh)

This script handles:

- Installing required CRDs (ServiceMonitors, PrometheusRules, PodMonitors, Routes)
- Installing the HyperShift operator with Azure-specific configuration
- Configuring external DNS integration
- Setting up managed service configuration for ARO-HCP

### 9. Hosted Cluster Creation
**Goal**: Create the actual hosted OpenShift cluster using all the previously configured infrastructure.

**Automated Script**: [create_basic_hosted_cluster.sh](https://github.com/openshift/hypershift/blob/main/contrib/managed-azure/create_basic_hosted_cluster.sh)

This script handles:

- Creating the hosted cluster with all required Azure integrations
- Configuring networking (VNet, subnets, NSGs)
- Setting up marketplace image references
- Applying FIPS configuration and security settings
- Enabling workload identity and managed identity integration

### 10. Cleanup and Deletion

#### Automated Cleanup (Recommended)

If you used the automated setup scripts, you can use the corresponding deletion scripts for easy cleanup:

**Complete Cleanup** - Delete both hosted cluster and AKS management cluster:
```shell
../contrib/managed-azure/delete_all.sh
```

This is the recommended approach as it will clean up all cluster-specific resources in the correct order:
1. Hosted cluster and its managed resources
2. AKS management cluster and resource group  
3. Customer VNet and NSG resource groups
4. AKS-specific Key Vault role assignments

Note: Managed identities, service principals, and Key Vault itself are preserved for reuse across multiple clusters

**Individual Cleanup** - For more granular control:

Delete only the hosted cluster:
```shell
../contrib/managed-azure/delete_hosted_cluster.sh
```

Delete only the AKS management cluster:
```shell
../contrib/managed-azure/delete_aks_cluster.sh
```

#### Manual Deletion

You can also delete the hosted cluster manually using the hypershift CLI:
```shell
${HYPERSHIFT_BINARY_PATH}/hypershift destroy cluster azure \
--name $CLUSTER_NAME \
--azure-creds $AZURE_CREDS \
--resource-group-name ${MANAGED_RG_NAME}
```

!!! tip
    If you used the automated scripts, `CLUSTER_NAME` is set to `"${PREFIX}-hc"`, `AZURE_CREDS` matches your `user-vars.sh` configuration, and `MANAGED_RG_NAME` is `"${PREFIX}-managed-rg"`.

!!! warning
    The manual deletion command only removes the hosted cluster. You'll need to manually clean up the AKS management cluster and other Azure resources created during setup.

!!! note
    The automated deletion scripts include safety confirmations and handle resource dependencies correctly. Some deletions may take several minutes to complete in the background.

!!! important
    **Resource Reuse**: The deletion scripts preserve managed identities, service principals, and the Key Vault itself for reuse across multiple clusters. AKS-specific role assignments are removed, but the underlying identities and vault remain for reuse.

## Troubleshooting

If you encounter issues with the automated scripts:

1. **Check prerequisites**: Ensure all required tools are installed and configured
2. **Verify permissions**: Confirm your service principal has the required permissions
3. **Review logs**: The scripts use `set -x` for detailed logging
4. **Partial execution**: Run individual scripts directly from the contrib/managed-azure folder if you need to resume from a specific point
5. **Manual verification**: Use the Azure portal to verify resources were created correctly

For additional help, reach out to #project-hypershift on Red Hat Slack.
