---
title: Getting Started with AWS
description: Deploy your first HyperShift hosted cluster on Amazon Web Services
---

# Getting Started with HyperShift on AWS

AWS is the most mature and feature-complete platform for HyperShift. This guide will help you deploy your first hosted cluster using the approach that best fits your needs.

## Choose Your Deployment Model

=== "ROSA HCP (Recommended)"

    **Red Hat OpenShift Service on AWS with Hosted Control Planes**

    - ✅ **Fully Managed** - Red Hat handles infrastructure and operations
    - ✅ **Quick Setup** - Clusters ready in 10-15 minutes
    - ✅ **Enterprise Support** - 24/7 Red Hat support included
    - ✅ **Integrated Billing** - Single AWS bill

    Perfect for: Production workloads, teams wanting managed services

=== "Self-Managed"

    **Self-Managed HyperShift on AWS**

    - 🛠️ **Full Control** - You manage the infrastructure
    - 🔧 **Customizable** - Complete control over configurations
    - 💰 **Cost Flexible** - Pay only for AWS infrastructure
    - 📋 **More Setup** - Requires manual configuration

    Perfect for: Custom requirements, existing infrastructure, development

## ROSA HCP Quickstart (Managed)

### Prerequisites

- AWS CLI configured with appropriate permissions
- Red Hat account and subscription

### Step 1: Install ROSA CLI

```bash
# Download and install ROSA CLI
curl -LO https://github.com/openshift/rosa/releases/latest/download/rosa-linux.tar.gz
tar -xzf rosa-linux.tar.gz
sudo mv rosa /usr/local/bin/
rosa version
```

### Step 2: Initialize ROSA

```bash
# Log into your Red Hat account
rosa login

# Initialize ROSA in your AWS account
rosa init

# Verify your account is ready
rosa verify quota
rosa verify permissions
```

### Step 3: Create Your First Hosted Cluster

```bash
# Create a ROSA HCP cluster
rosa create cluster \
  --cluster-name my-first-hcp \
  --mode hosted-cp \
  --region us-east-1 \
  --compute-machine-type m5.large \
  --replicas 3 \
  --yes
```

### Step 4: Access Your Cluster

```bash
# Wait for cluster creation (10-15 minutes)
rosa describe cluster my-first-hcp

# Create admin user when ready
rosa create admin --cluster my-first-hcp

# Get login command
rosa describe cluster my-first-hcp --output json | jq -r '.console.url'
```

🎉 **Your ROSA HCP cluster is ready!** Access the OpenShift console using the provided URL.

## Self-Managed Quickstart

### Prerequisites

Before you begin, ensure you have:

- [x] **OpenShift cluster** (4.14+) as your management cluster
- [x] **AWS CLI** configured with credentials
- [x] **oc CLI** logged into your management cluster
- [x] **Route53 hosted zone** for your domain
- [x] **Pull secret** from [Red Hat Console](https://cloud.redhat.com/openshift/install/aws/installer-provisioned)

### Step 1: Install HyperShift CLI

!!! important "CLI Tool Support"

    - **`hcp` CLI**: Officially supported for production use and cluster management
    - **`hypershift` CLI**: Developer-only tool, not supported for production. Used primarily for operator installation and development workflows

Choose one of these methods:

=== "From Red Hat Console (ROSA HCP)"

    For managed ROSA HCP deployments:

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

### Step 2: Set Up AWS Prerequisites

#### Create S3 Bucket for OIDC

```bash
# Choose a unique bucket name
export BUCKET_NAME="hypershift-oidc-$(whoami)-$(date +%s)"
export AWS_REGION="us-east-1"

# Create bucket
aws s3api create-bucket \
  --bucket $BUCKET_NAME \
  --region $AWS_REGION

# Configure public access for OIDC
aws s3api delete-public-access-block --bucket $BUCKET_NAME

# Create bucket policy for OIDC access
cat << EOF > /tmp/bucket-policy.json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": "*",
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::${BUCKET_NAME}/*"
    }
  ]
}
EOF

aws s3api put-bucket-policy --bucket $BUCKET_NAME --policy file:///tmp/bucket-policy.json
```

#### Set Up Route53 Hosted Zone

```bash
# Set your base domain
export BASE_DOMAIN="example.com"  # Replace with your domain

# Create hosted zone (if you don't have one)
aws route53 create-hosted-zone \
  --name $BASE_DOMAIN \
  --caller-reference $(date +%s)
```

### Step 3: Install HyperShift Operator

```bash
# Set environment variables
export AWS_CREDS="$HOME/.aws/credentials"

# Install HyperShift operator on your management cluster
hypershift install \
  --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
  --oidc-storage-provider-s3-credentials $AWS_CREDS \
  --oidc-storage-provider-s3-region $AWS_REGION \
  --enable-defaulting-webhook true

# Verify installation
oc get pods -n hypershift
```

### Step 4: Create Your First Hosted Cluster

```bash
# Set cluster configuration
export CLUSTER_NAME="my-first-cluster"
export PULL_SECRET_FILE="$HOME/pull-secret.json"  # Download from Red Hat Console

# Create the hosted cluster
hypershift create cluster aws \
  --name $CLUSTER_NAME \
  --node-pool-replicas 3 \
  --base-domain $BASE_DOMAIN \
  --pull-secret $PULL_SECRET_FILE \
  --aws-creds $AWS_CREDS \
  --region $AWS_REGION \
  --generate-ssh
```

### Step 5: Monitor and Access Your Cluster

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

## Common Next Steps

Once your cluster is running, you might want to:

### Scale Your Cluster

```bash
# Add more worker nodes
oc scale nodepool/$CLUSTER_NAME-nodepool-1 \
  --namespace clusters \
  --replicas=5

# Create additional node pools with different instance types
hypershift create nodepool aws \
  --cluster-name $CLUSTER_NAME \
  --name gpu-workers \
  --instance-type g4dn.xlarge \
  --replicas 2
```

### Deploy Applications

```bash
# Create a test application
oc new-project hello-world
oc new-app --image=quay.io/quay/busybox:latest --name=test-app
oc get pods -w
```

### Access Advanced Features

- **[ARM Workers](../how-to/aws/create-aws-hosted-cluster-arm-workers.md)**: Deploy ARM-based worker nodes
- **[Private Clusters](../how-to/aws/deploy-aws-private-clusters.md)**: Create clusters without public endpoints
- **[AutoNode](../how-to/aws/create-aws-hosted-cluster-autonode.md)**: Enable Karpenter-based auto-scaling
- **[Multiple Zones](../how-to/aws/create-aws-hosted-cluster-multiple-zones.md)**: Deploy across multiple availability zones

## Troubleshooting

### Common Issues

??? question "Cluster stuck in 'Pending' state"

    **Symptoms**: HostedCluster shows "Pending" status for more than 10 minutes

    **Solutions**:
    ```bash
    # Check cluster events
    oc describe hostedcluster $CLUSTER_NAME -n clusters

    # Check operator logs
    oc logs -n hypershift deployment/operator -f

    # Verify AWS permissions and quotas
    aws iam simulate-principal-policy --policy-source-arn $(aws sts get-caller-identity --query Arn --output text) --action-names ec2:RunInstances --resource-arns "*"
    ```

??? question "Cannot access cluster API"

    **Symptoms**: `oc` commands timeout or fail to connect

    **Solutions**:
    ```bash
    # Check API endpoint status
    oc get hostedcluster $CLUSTER_NAME -n clusters -o jsonpath='{.status.kubeAPIServer.host}'

    # Verify DNS resolution
    nslookup api.$CLUSTER_NAME.$BASE_DOMAIN

    # Check load balancer status in AWS console
    ```

??? question "Node pools not scaling"

    **Symptoms**: Nodes not joining the cluster

    **Solutions**:
    ```bash
    # Check nodepool status
    oc describe nodepool -n clusters

    # Verify machine sets in hosted cluster
    export KUBECONFIG=$CLUSTER_NAME-kubeconfig
    oc get machinesets -A

    # Check AWS EC2 instances
    aws ec2 describe-instances --filters "Name=tag:kubernetes.io/cluster/$CLUSTER_NAME,Values=owned"
    ```

### Get Help

- 📚 **AWS-specific guides**: [How-to guides](../how-to/aws/)
- 🔧 **Troubleshooting**: [AWS troubleshooting](../how-to/aws/troubleshooting/)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)

## Cleanup

When you're done experimenting:

```bash
# Delete the hosted cluster
hypershift destroy cluster aws \
  --name $CLUSTER_NAME \
  --aws-creds $AWS_CREDS

# Clean up AWS resources (optional)
aws s3 rb s3://$BUCKET_NAME --force
aws route53 delete-hosted-zone --id $(aws route53 list-hosted-zones --query "HostedZones[?Name=='$BASE_DOMAIN.'].Id" --output text)
```

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster on AWS. Explore the [how-to guides](../how-to/) to learn about advanced features and configurations.