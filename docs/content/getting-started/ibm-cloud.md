---
title: Getting Started with IBM Cloud
description: Deploy your first HyperShift hosted cluster on IBM Cloud PowerVS
---

# Getting Started with HyperShift on IBM Cloud

IBM Cloud PowerVS provides a unique platform for hosting OpenShift control planes on POWER architecture, offering high-performance computing capabilities for memory-intensive and specialized workloads.

## Choose Your Deployment Model

=== "PowerVS (Self-Managed Only)"

    **IBM Cloud PowerVS with HyperShift**

    - 🚀 **Power Architecture** - POWER9/10 processors for high-performance workloads
    - 🛠️ **Self-Managed** - You control the infrastructure and operations
    - 💪 **High Performance** - Optimized for memory-intensive applications
    - 🔧 **Customizable** - Complete control over configurations
    - ⚠️ **Specialized** - Best for Power-specific applications and workloads

    Perfect for: High-performance computing, databases, memory-intensive applications, Power-specific workloads

!!! note "Single Deployment Option"

    Unlike AWS and Azure, IBM Cloud PowerVS only supports self-managed HyperShift deployments. There is no managed service option currently available.

## PowerVS Quickstart

### Prerequisites

Before you begin, ensure you have:

- [x] **OpenShift management cluster** (4.8+) with admin access
- [x] **IBM Cloud CLI** configured with API key
- [x] **oc CLI** logged into your management cluster
- [x] **CIS Domain** in IBM Cloud Internet Services for DNS
- [x] **Pull secret** from [Red Hat Console](https://console.redhat.com/openshift/install/ibm-cloud)
- [x] **IBM Cloud API Key** with required permissions

### Step 1: Install HyperShift CLI

!!! important "CLI Tool Support"

    - **`hcp` CLI**: Officially supported for production use and cluster management
    - **`hypershift` CLI**: Developer-only tool, not supported for production. Used primarily for operator installation and development workflows

Choose one of these methods:

=== "From MCE Console (Recommended)"

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

### Step 2: Set Up IBM Cloud Authentication

Configure your IBM Cloud credentials:

```bash
# Set your IBM Cloud API Key
export IBMCLOUD_API_KEY="your-api-key-here"

# Alternatively, create a credentials file
cat <<EOF > $HOME/ibmcloud-credentials
your-api-key-here
EOF
export IBMCLOUD_CREDENTIALS="$HOME/ibmcloud-credentials"
```

!!! warning "Required IBM Cloud Permissions"

    Your API Key must have these service roles:

    | Service | Roles |
    |---------|-------|
    | Workspace for Power Systems Virtual Server | Manager, Administrator |
    | VPC Infrastructure Services | Manager, Administrator |
    | Internet Services | Manager, Administrator |
    | Direct Link | Viewer |
    | IAM Identity Service | User API key creator, Service ID creator, Administrator |
    | All account management services | Administrator |
    | All Identity and Access enabled services | Manager, Editor |
    | Cloud Object Storage | Manager, Administrator |
    | Transit Gateway | Manager, Editor |

### Step 3: Install HyperShift Operator

Install HyperShift on your management cluster:

```bash
# Install HyperShift operator and CRDs
hypershift install

# Verify installation
oc get pods -n hypershift
```

### Step 4: Prepare Configuration

Set up your cluster configuration variables:

```bash
# Cluster configuration
export CLUSTER_NAME="my-power-cluster"
export REGION="tok"                    # PowerVS region (e.g., tok, dal, fra)
export ZONE="tok04"                    # PowerVS zone within region
export VPC_REGION="jp-tok"             # VPC region for networking
export TRANSIT_GATEWAY_LOCATION="ap-north"  # Transit gateway location

# Domain and credentials
export BASE_DOMAIN="hypershift-on-power.com"  # Your existing CIS domain
export RESOURCE_GROUP="ibm-hypershift-dev"    # IBM Cloud resource group
export PULL_SECRET="$HOME/pull-secret.json"

# Release image (get latest from https://multi.ocp.releases.ci.openshift.org)
export RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:4.15.0-0.nightly-multi-2024-01-15-123456"
```

!!! tip "Finding the Right Values"

    - **Regions and Zones**: Check the [IBM Cloud region mapping](https://cluster-api-ibmcloud.sigs.k8s.io/reference/regions-zones-mapping.html)
    - **Release Images**: Get the latest multi-architecture nightly builds from [multi.ocp.releases.ci.openshift.org](https://multi.ocp.releases.ci.openshift.org)
    - **Base Domain**: Must be an existing domain in [IBM Cloud Internet Services](https://cloud.ibm.com/docs/cis)

### Step 5: Create Your First Hosted Cluster

```bash
# Create the PowerVS hosted cluster
hypershift create cluster powervs \
  --name $CLUSTER_NAME \
  --region $REGION \
  --zone $ZONE \
  --vpc-region $VPC_REGION \
  --base-domain $BASE_DOMAIN \
  --resource-group $RESOURCE_GROUP \
  --release-image $RELEASE_IMAGE \
  --pull-secret $PULL_SECRET \
  --node-pool-replicas=2 \
  --transit-gateway-location $TRANSIT_GATEWAY_LOCATION
```

!!! important "Unique Cluster Names"

    Cluster names must be unique across different management clusters when using the `--recreate-secrets` flag, as this recreates service IDs and credentials.

### Step 6: Monitor Cluster Creation

```bash
# Monitor cluster creation progress
oc get hostedcluster -n clusters -w

# Check when cluster is ready
oc get hostedcluster $CLUSTER_NAME -n clusters

# Get cluster kubeconfig when ready
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
export KUBECONFIG=$CLUSTER_NAME-kubeconfig

# Verify access to your hosted cluster
oc get nodes
oc get clusterversion
```

## Advanced Configuration

### Creating Infrastructure Separately

For more control over infrastructure, you can create resources separately:

```bash
# Create infrastructure first
hypershift create infra powervs \
  --name $CLUSTER_NAME \
  --region $REGION \
  --zone $ZONE \
  --vpc-region $VPC_REGION \
  --base-domain $BASE_DOMAIN \
  --resource-group $RESOURCE_GROUP \
  --transit-gateway-location $TRANSIT_GATEWAY_LOCATION

# Then create the cluster using existing infrastructure
hypershift create cluster powervs \
  --name $CLUSTER_NAME \
  --infra-id $CLUSTER_NAME \
  --release-image $RELEASE_IMAGE \
  --pull-secret $PULL_SECRET \
  --node-pool-replicas=2
```

👉 **Complete Guide**: [Create PowerVS Infrastructure Separately](../how-to/powervs/create-infra-separately.md)

### Custom Endpoints

Configure custom IBM Cloud service endpoints if needed:

```bash
# Set custom endpoints (optional)
export IBMCLOUD_POWER_API_ENDPOINT="custom-power-endpoint"
export IBMCLOUD_VPC_API_ENDPOINT="custom-vpc-endpoint"
export IBMCLOUD_PLATFORM_API_ENDPOINT="custom-platform-endpoint"
export IBMCLOUD_COS_API_ENDPOINT="custom-cos-endpoint"
```

### Recreating Secrets

When rerunning cluster creation commands:

```bash
# Use --recreate-secrets flag when rerunning commands
hypershift create cluster powervs \
  --name $CLUSTER_NAME \
  --recreate-secrets \
  [... other parameters ...]
```

!!! warning "Recreate Secrets Flag"

    The `--recreate-secrets` flag removes existing service IDs and recreates credentials. Only use this when rerunning commands, and ensure cluster names are unique across management clusters.

## Common Next Steps

Once your cluster is running, you might want to:

### Scale Your Cluster

```bash
# Add more worker nodes
oc scale nodepool/$CLUSTER_NAME-nodepool-1 \
  --namespace clusters \
  --replicas=4
```

### Deploy Power-Optimized Applications

```bash
# Create a test application optimized for Power architecture
oc new-project power-app
oc new-app --image=registry.redhat.io/ubi8/ubi:latest --name=power-test
oc get pods -w
```

### Access Advanced Features

- **[Infrastructure Creation](../how-to/powervs/create-infra-separately.md)**: Create PowerVS infrastructure separately
- **[Prerequisites Guide](../how-to/powervs/prerequisites-and-env-guide.md)**: Detailed setup requirements
- **[Global Pull Secret](../how-to/powervs/global-pull-secret.md)**: Configure global registry access

## Troubleshooting

### Common Issues

??? question "API Key permissions errors"

    **Symptoms**: Cluster creation fails with authorization errors

    **Solutions**:
    ```bash
    # Verify API Key permissions in IBM Cloud Console
    # Ensure all required service roles are assigned
    # Check resource group access permissions

    # Test API key access
    ibmcloud iam api-keys
    ibmcloud iam user-policies $YOUR_USER_ID
    ```

??? question "CIS Domain not found"

    **Symptoms**: DNS configuration fails during cluster creation

    **Solutions**:
    ```bash
    # Verify CIS domain exists and is active
    ibmcloud cis domains --instance $CIS_INSTANCE_NAME

    # Check domain status in IBM Cloud Internet Services
    # Ensure domain is properly configured with DNS records
    ```

??? question "PowerVS resource quota exceeded"

    **Symptoms**: Cluster creation fails due to resource limits

    **Solutions**:
    ```bash
    # Check PowerVS quotas in IBM Cloud Console
    # Request quota increases if needed
    # Consider using different regions/zones with available capacity

    # Review resource requirements in the region
    ibmcloud pi workspace list
    ```

??? question "Transit Gateway connectivity issues"

    **Symptoms**: Cluster components cannot communicate

    **Solutions**:
    ```bash
    # Verify Transit Gateway configuration
    # Check route tables and connection status
    # Ensure proper network ACLs and security groups

    # Check Transit Gateway status
    ibmcloud tg gateways
    ibmcloud tg connections $GATEWAY_ID
    ```

### Get Help

- 📚 **PowerVS-specific guides**: [How-to guides](../how-to/powervs/)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)
- 🏢 **IBM Support**: [IBM Cloud Support](https://cloud.ibm.com/unifiedsupport/supportcenter)

## Cleanup

When you're done experimenting:

```bash
# Delete the hosted cluster
hypershift destroy cluster powervs \
  --name $CLUSTER_NAME \
  --ibmcloud-credentials $IBMCLOUD_CREDENTIALS

# Clean up PowerVS and VPC resources manually if needed
# Review IBM Cloud Console for any remaining resources
```

!!! warning "Resource Cleanup"

    PowerVS resources may not be automatically cleaned up in all cases. Review your IBM Cloud Console to ensure all resources are properly deleted to avoid ongoing charges.

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster on IBM Cloud PowerVS. This platform is ideal for high-performance computing workloads that benefit from POWER architecture capabilities.