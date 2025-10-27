---
title: Getting Started with Azure
description: Deploy your first HyperShift hosted cluster on Microsoft Azure
---

# Getting Started with HyperShift on Azure

Azure is fully supported for HyperShift through ARO-HCP (Azure Red Hat OpenShift with Hosted Control Planes). This guide will help you deploy your first hosted cluster using the approach that best fits your needs.

## Choose Your Deployment Model

=== "ARO-HCP (Managed) - Recommended"

    **Azure Red Hat OpenShift with Hosted Control Planes**

    - ✅ **Fully Managed** - Microsoft and Red Hat handle infrastructure and operations
    - ✅ **Quick Setup** - Clusters ready in 10-15 minutes
    - ✅ **Enterprise Support** - Microsoft and Red Hat support included
    - ✅ **Azure Integration** - Native Azure AD, monitoring, and compliance
    - ✅ **Automated Setup** - Scripts handle complex Azure setup automatically

    Perfect for: Production workloads, teams wanting fully managed services

=== "Self-Managed (Developer Preview)"

    **Self-Managed HyperShift on Azure**

    - 🛠️ **Full Control** - You manage the OpenShift management cluster
    - 🔧 **Customizable** - Complete control over configurations
    - 💰 **Cost Flexible** - Pay only for Azure infrastructure
    - 📋 **More Setup** - Requires manual configuration
    - ⚠️ **Developer Preview** - Available in OCP 4.21+

    Perfect for: Custom requirements, existing infrastructure, development

## ARO-HCP Quickstart (Managed)

### Prerequisites

- Azure CLI configured with subscription access
- Contributor + User Access Administrator roles
- Red Hat account and subscription

### Step 1: Clone HyperShift Repository

```bash
# Clone the repository with automation scripts
git clone https://github.com/openshift/hypershift.git
cd hypershift/contrib/managed-azure
```

### Step 2: Configure Your Environment

Create your configuration file:

```bash
cat <<EOF > user-vars.sh
# User variables - customize these values
export PREFIX="YOUR-management"
export PULL_SECRET="/path/to/pull-secret.txt"
export HYPERSHIFT_BINARY_PATH="/path/to/hypershift/bin/"
export HYPERSHIFT_IMAGE="quay.io/hypershift/hypershift-operator:latest"
export RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:4.20.0-ec.3-multi"
export LOCATION="eastus"
export AZURE_CREDS="/path/to/azure-creds.json"
# Storage account names: 3-24 chars, numbers and lowercase letters only
export OIDC_ISSUER_NAME="YOURmanagement"
EOF
```

### Step 3: Create Azure Credentials

Create a service principal for HyperShift:

```bash
# Get Azure account details
ACCOUNT_DETAILS=$(az account show --query '{subscriptionId: id, tenantId: tenantId}' -o json)
SUBSCRIPTION_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.subscriptionId')
TENANT_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.tenantId')

# Create service principal
SP_DETAILS=$(az ad sp create-for-rbac \
  --name "${PREFIX}-sp" \
  --role Contributor \
  --scopes "/subscriptions/$SUBSCRIPTION_ID" \
  -o json)

CLIENT_ID=$(echo "$SP_DETAILS" | jq -r '.appId')
CLIENT_SECRET=$(echo "$SP_DETAILS" | jq -r '.password')

# Create credentials file
cat <<EOF > azure-creds.json
{
  "subscriptionId": "$SUBSCRIPTION_ID",
  "tenantId": "$TENANT_ID",
  "clientId": "$CLIENT_ID",
  "clientSecret": "$CLIENT_SECRET"
}
EOF
```

!!! warning "Service Principal Permissions"

    Your service principal needs the Microsoft Graph `Application.ReadWrite.OwnedBy` permission and User Access Administrator role at the subscription level. In most cases, you'll need to submit a DPTP request to have this configured.

### Step 4: Run Automated Setup

For your **first cluster** (includes one-time resource setup):

```bash
# Complete automated setup with first-time resources
./setup_all.sh --first-time
```

For **additional clusters** (reuses existing resources):

```bash
# Setup additional clusters
./setup_all.sh
```

!!! note "Automatic Authentication"

    The setup script automatically logs you into Azure if you're not already authenticated. No separate login step is required!

!!! warning "One-Time Setup Components"

    Three resources are created once and reused across multiple clusters to avoid quota issues: service principals and Key Vault, OIDC issuer, and data plane identities. Use `--first-time` only for your first cluster setup.

### Step 5: Monitor Cluster Creation

```bash
# Wait for cluster creation (10-15 minutes)
kubectl get hostedcluster -n clusters -w

# Check when cluster is ready
kubectl get hostedcluster ${PREFIX}-hc -n clusters

# Get cluster kubeconfig when ready
${HYPERSHIFT_BINARY_PATH}/hypershift create kubeconfig \
  --name ${PREFIX}-hc > ${PREFIX}-hc-kubeconfig

export KUBECONFIG=${PREFIX}-hc-kubeconfig

# Verify access to your hosted cluster
kubectl get nodes
kubectl get clusterversion
```

🎉 **Your ARO-HCP cluster is ready!** Access your cluster using the generated kubeconfig.

## Self-Managed Quickstart (Developer Preview)

!!! note "Developer Preview in OCP 4.21"

    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

### Prerequisites

Before you begin, ensure you have:

- [x] **Azure OpenShift cluster** (4.21+) as your management cluster
- [x] **Azure CLI** configured with credentials
- [x] **oc CLI** logged into your management cluster
- [x] **Azure DNS zone** for your domain delegation
- [x] **Pull secret** from [Red Hat Console](https://cloud.redhat.com/openshift/install/aws/installer-provisioned)
- [x] **Contributor + User Access Administrator** Azure roles

### Step 1: Install HyperShift CLI

!!! important "CLI Tool Support"

    - **`hcp` CLI**: Officially supported for production use and cluster management
    - **`hypershift` CLI**: Developer-only tool, not supported for production. Used primarily for operator installation and development workflows

Choose one of these methods:

=== "From Red Hat Console (ARO-HCP)"

    For managed ARO-HCP deployments:

    ```bash
    # Download the HCP CLI from Red Hat Console
    # Visit: https://console.redhat.com/openshift/downloads
    # Select "Command Line Tools" and download the HCP CLI for your platform

    # Example for Linux:
    # wget <download-link-from-console>
    # tar -xzf hcp-linux-amd64.tar.gz
    # sudo mv hcp /usr/local/bin/
    # hcp version
    ```

=== "From MCE Console (Self-Managed)"

    For self-managed deployments with MCE/ACM:

    ```bash
    # Access your MCE/ACM console and navigate to:
    # Infrastructure > Clusters > Create cluster > Hosted control plane
    # The console provides download links for the HCP CLI

    # Follow the download instructions provided in the console
    ```

=== "Build from Source"

    **Prerequisites:**
    - Go 1.24+ installed
    - Git installed
    - Make installed

    ```bash
    # Clone the repository
    git clone https://github.com/openshift/hypershift.git
    cd hypershift

    # Build hypershift CLI (developer tool only)
    make hypershift

    # Install the CLI tool
    sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift

    # Verify installation
    hypershift version
    ```

### Step 2: Azure Workload Identity Setup

Set up the foundational security infrastructure:

```bash
# Set environment variables
export LOCATION="eastus"
export RESOURCE_GROUP_NAME="os4-common"  # or your persistent RG name
export OIDC_ISSUER_NAME="hypershift-oidc-issuer"

# Follow the complete workload identity setup guide
```

👉 **Complete Guide**: [Azure Workload Identity Setup](../how-to/azure/azure-workload-identity-setup.md)

### Step 3: Management Cluster Setup

Configure DNS and install HyperShift operator:

```bash
# Set DNS configuration
export BASE_DOMAIN="example.com"  # Replace with your domain
export DNS_ZONE_RESOURCE_GROUP="os4-common"

# Follow the management cluster setup guide
```

👉 **Complete Guide**: [Setup Azure Management Cluster for HyperShift](../how-to/azure/setup-management-cluster.md)

### Step 4: Create Your First Hosted Cluster

```bash
# Set cluster configuration
export CLUSTER_NAME="my-first-cluster"
export PULL_SECRET_FILE="$HOME/pull-secret.json"

# Follow the self-managed cluster creation guide
```

👉 **Complete Guide**: [Create a Self-Managed Azure HostedCluster](../how-to/azure/create-self-managed-azure-cluster.md)

## Common Next Steps

Once your cluster is running, you might want to:

### Scale Your Cluster

```bash
# Add more worker nodes (ARO-HCP)
kubectl scale nodepool/${PREFIX}-hc-nodepool-1 \
  --namespace clusters \
  --replicas=5

# For self-managed, follow nodepool scaling in the detailed guides
```

### Deploy Applications

```bash
# Create a test application
kubectl new-project hello-world
kubectl new-app --image=quay.io/quay/busybox:latest --name=test-app
kubectl get pods -w
```

### Access Advanced Features

- **[Custom Networking](../how-to/azure/create-self-managed-azure-cluster.md)**: Configure custom VNets and security groups
- **[Workload Identity](../how-to/azure/azure-workload-identity-setup.md)**: Advanced Azure AD integration
- **[Managed Options](../how-to/azure/create-azure-cluster-with-options.md)**: ARO-HCP configuration options

## Troubleshooting

### Common Issues

??? question "Cluster stuck in 'Pending' state"

    **Symptoms**: HostedCluster shows "Pending" status for more than 10 minutes

    **Solutions**:
    ```bash
    # Check cluster events
    kubectl describe hostedcluster $CLUSTER_NAME -n clusters

    # For ARO-HCP, check automation scripts logs
    # Check service principal permissions and Azure quotas
    az role assignment list --assignee $CLIENT_ID
    ```

??? question "Cannot access cluster API"

    **Symptoms**: `kubectl` commands timeout or fail to connect

    **Solutions**:
    ```bash
    # Check API endpoint status
    kubectl get hostedcluster $CLUSTER_NAME -n clusters -o jsonpath='{.status.kubeAPIServer.host}'

    # Verify DNS resolution
    nslookup api.$CLUSTER_NAME.$BASE_DOMAIN

    # Check Azure Load Balancer status in Azure portal
    ```

??? question "Workload Identity authentication failures"

    **Symptoms**: Pods cannot access Azure services

    **Solutions**:
    ```bash
    # Check federated identity credentials
    az identity federated-credential list \
      --identity-name $IDENTITY_NAME \
      --resource-group $RESOURCE_GROUP_NAME

    # Verify OIDC issuer configuration
    az storage blob list \
      --account-name $OIDC_STORAGE_ACCOUNT \
      --container-name '$web'
    ```

### Get Help

- 📚 **Azure-specific guides**: [How-to guides](../how-to/azure/)
- 🔧 **Troubleshooting**: [Azure troubleshooting](../how-to/azure/troubleshooting/)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)

## Cleanup

### ARO-HCP Cleanup

When you're done with your managed cluster:

```bash
# Complete cleanup using automation scripts
cd hypershift/contrib/managed-azure
./delete_all.sh

# Or delete only the hosted cluster
./delete_hosted_cluster.sh
```

### Self-Managed Cleanup

```bash
# Delete the hosted cluster
hypershift destroy cluster azure \
  --name $CLUSTER_NAME \
  --azure-creds $AZURE_CREDS \
  --resource-group-name ${CLUSTER_NAME}-managed-rg

# Clean up workload identities and OIDC issuer manually if no longer needed
```

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster on Azure. Explore the [how-to guides](../how-to/) to learn about advanced features and configurations.