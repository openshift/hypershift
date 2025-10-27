---
title: Getting Started with OpenStack
description: Deploy your first HyperShift hosted cluster on OpenStack private cloud infrastructure
---

# Getting Started with HyperShift on OpenStack

OpenStack provides a robust private cloud platform for hosting HyperShift control planes. While currently in developer preview, it offers excellent capabilities for development, testing, and proof-of-concept deployments on private cloud infrastructure.

## Choose Your Deployment Model

=== "OpenStack (Self-Managed Only)"

    **Private Cloud Infrastructure with HyperShift**

    - ✅ **Private Cloud** - Deploy on your existing OpenStack infrastructure
    - ✅ **Development Ready** - Perfect for testing and development environments
    - ✅ **Flexible Configuration** - Support for various OpenStack configurations
    - ✅ **Resource Control** - Leverage existing OpenStack quotas and resources
    - 🛠️ **Self-Managed** - You control the OpenStack and management cluster
    - ⚠️ **Developer Preview** - Currently not intended for production use

    Perfect for: Development environments, testing, private cloud deployments, OpenStack-based infrastructure

!!! warning "Developer Preview Status"

    OpenStack support within HyperShift is currently in developer preview and is not yet intended for production use. However, it works reliably for development and testing purposes.

## OpenStack Quickstart

### Prerequisites

Before you begin, ensure you have:

- [x] **OpenShift management cluster** (4.17+) with admin access running OVN-Kubernetes
- [x] **OpenStack cloud** with admin credentials and quotas
- [x] **Load balancer backend** (e.g., Octavia) in the management cluster
- [x] **OpenStack Octavia service** running for ingress load balancers
- [x] **Network connectivity** between management cluster and OpenStack cloud
- [x] **Pull secret** from [Red Hat Console](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned)

### Step 1: Install HyperShift CLI Tools

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

    # Build both hypershift and hcp CLI tools
    make hypershift product-cli

    # Install the CLI tools
    sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift
    sudo install -m 0755 bin/hcp /usr/local/bin/hcp

    # Verify installation
    hypershift version
    hcp version
    ```

### Step 2: Install HyperShift Operator

Deploy the HyperShift operator with tech preview support:

```bash
# Install HyperShift operator with OpenStack support
hypershift install --tech-preview-no-upgrade

# Verify installation
oc get pods -n hypershift

# Example output:
# NAME                        READY   STATUS    RESTARTS   AGE
# operator-755d587f44-lrtrq   1/1     Running   0          114s
# operator-755d587f44-qj6pz   1/1     Running   0          114s
```

!!! note "Tech Preview Flag"

    OpenStack support is behind a feature gate, requiring the `--tech-preview-no-upgrade` flag. Once the platform is GA, this flag won't be needed.

### Step 3: Prepare OpenStack Environment

Set up your OpenStack environment and credentials:

```bash
# Configure OpenStack credentials (clouds.yaml)
export OS_CLOUD="openstack"
export CLOUDS_YAML="$HOME/.config/openstack/clouds.yaml"

# Set OpenStack configuration variables
export EXTERNAL_NETWORK_ID="your-external-network-id"
export FLAVOR="m1.large"  # Flavor for worker nodes
export DNS_NAMESERVERS="8.8.8.8,1.1.1.1"  # DNS servers for subnet

# Optional: CA certificate for self-signed OpenStack APIs
export CA_CERT_PATH="$HOME/ca.crt"
```

!!! tip "OpenStack Configuration"

    Ensure your `clouds.yaml` file is properly configured with your OpenStack credentials. This file is typically located at `~/.config/openstack/clouds.yaml`.

### Step 4: Prepare RHCOS Image (Optional)

You can upload a RHCOS image to OpenStack or let ORC manage it automatically:

=== "Automatic (Recommended)"

    ```bash
    # ORC will automatically download and manage RHCOS images
    # No additional steps needed - skip to cluster creation
    ```

=== "Manual Upload"

    ```bash
    # Download RHCOS image from OpenShift mirror
    export RHCOS_VERSION="4.17.0"
    curl -LO "https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.17/${RHCOS_VERSION}/rhcos-openstack.x86_64.qcow2"

    # Upload to OpenStack
    openstack image create \
      --disk-format qcow2 \
      --file rhcos-openstack.x86_64.qcow2 \
      rhcos-4.17.0

    # Use the image name when creating cluster
    export RHCOS_IMAGE_NAME="rhcos-4.17.0"
    ```

### Step 5: Configure Network and DNS (Optional)

Set up ingress networking:

```bash
# Create floating IP for ingress (optional)
export INGRESS_FLOATING_IP=$(openstack floating ip create $EXTERNAL_NETWORK_ID -f value -c floating_ip_address)

echo "Created floating IP: $INGRESS_FLOATING_IP"
echo "Add DNS record: *.apps.<cluster-name>.<base-domain> -> $INGRESS_FLOATING_IP"
```

!!! tip "DNS Configuration"

    If you pre-create a floating IP for ingress, create a DNS wildcard record pointing `*.apps.<cluster-name>.<base-domain>` to the floating IP address.

### Step 6: Create Your First Hosted Cluster

Configure and create your OpenStack hosted cluster:

```bash
# Set cluster configuration
export CLUSTER_NAME="example"
export BASE_DOMAIN="openstack.lab"
export PULL_SECRET="$HOME/pull-secret.json"
export WORKER_COUNT="2"
export SSH_KEY="$HOME/.ssh/id_rsa.pub"

# Create the hosted cluster
hcp create cluster openstack \
  --name $CLUSTER_NAME \
  --base-domain $BASE_DOMAIN \
  --node-pool-replicas $WORKER_COUNT \
  --pull-secret $PULL_SECRET \
  --ssh-key $SSH_KEY \
  --openstack-credentials-file $CLOUDS_YAML \
  --openstack-external-network-id $EXTERNAL_NETWORK_ID \
  --openstack-node-flavor $FLAVOR \
  --openstack-dns-nameservers $DNS_NAMESERVERS \
  ${INGRESS_FLOATING_IP:+--openstack-ingress-floating-ip $INGRESS_FLOATING_IP} \
  ${CA_CERT_PATH:+--openstack-ca-cert-file $CA_CERT_PATH} \
  ${RHCOS_IMAGE_NAME:+--openstack-node-image-name $RHCOS_IMAGE_NAME}
```

!!! note "High Availability"

    The HCP CLI enables high availability by default. Control plane pods are distributed across nodes with anti-affinity rules. If your management cluster has fewer than 3 workers, use `--control-plane-availability-policy SingleReplica`.

### Step 7: Monitor Cluster Creation

```bash
# Monitor hosted control plane deployment (takes 10-15 minutes)
oc get pods -n clusters-$CLUSTER_NAME -w

# Check hosted cluster status
oc get hostedcluster -n clusters

# Example ready state:
# NAME      VERSION   KUBECONFIG                PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
# example   4.17.0    example-admin-kubeconfig  Completed  True        False         The hosted control plane is available
```

### Step 8: Access Your Hosted Cluster

```bash
# Generate kubeconfig for hosted cluster
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig

# Access your hosted cluster
export KUBECONFIG=$CLUSTER_NAME-kubeconfig

# Verify access
oc get nodes
oc get clusterversion

# Get kubeadmin password
oc get --namespace clusters Secret/${CLUSTER_NAME}-kubeadmin-password -o jsonpath='{.data.password}' | base64 --decode

# Example output:
# NAME                  STATUS   ROLES    AGE   VERSION
# example-n6prw         Ready    worker   32m   v1.27.4+18eadca
# example-nc6g4         Ready    worker   32m   v1.27.4+18eadca
```

### Step 9: Configure Ingress (If Needed)

If you didn't pre-create a floating IP, configure ingress manually:

```bash
# Wait for router service to get external IP
oc --kubeconfig $CLUSTER_NAME-kubeconfig \
  -n openshift-ingress get service/router-default \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# Create DNS record for *.apps.$CLUSTER_NAME.$BASE_DOMAIN
# pointing to the returned IP address
```

🎉 **Your OpenStack hosted cluster is ready!** Access the console at `https://console-openshift-console.apps.$CLUSTER_NAME.$BASE_DOMAIN`

## Advanced Configuration

### Prepare etcd for Production Use

For production-like testing, configure etcd with local storage:

```bash
# Follow the local storage procedure for etcd performance
# This ensures etcd pods use fast local storage instead of network storage
```

👉 **Complete Guide**: [etcd Local Storage Configuration](../how-to/openstack/etcd-local-storage.md)

### Create Multi-AZ Node Pools

Deploy worker nodes across availability zones:

```bash
# Create a nodepool in specific AZ
hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name ${CLUSTER_NAME}-zone-a \
  --replicas 2 \
  --openstack-node-availability-zone nova

# Create another nodepool in different AZ
hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name ${CLUSTER_NAME}-zone-b \
  --replicas 2 \
  --openstack-node-availability-zone nova2
```

👉 **Complete Guide**: [Availability Zone Distribution](../how-to/openstack/az.md)

### Configure Additional Network Ports

Add specialized network interfaces to nodes:

```bash
# Create nodepool with additional ports
hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name ${CLUSTER_NAME}-sriov \
  --replicas 1 \
  --openstack-node-additional-port network-id=sriov-network-id,vnic-type=direct
```

👉 **Complete Guide**: [Additional Ports Configuration](../how-to/openstack/additional-ports.md)

### Scale Your Cluster

```bash
# Scale existing nodepool
oc scale nodepool/$CLUSTER_NAME \
  --namespace clusters \
  --replicas=5

# Verify scaling
oc get nodepools --namespace clusters
```

## Common Next Steps

Once your cluster is running, you might want to:

### Deploy Applications

```bash
# Create a test application
oc new-project test-app
oc new-app --image=quay.io/quay/busybox:latest --name=openstack-test
oc get pods -w
```

### Configure Performance Tuning

Optimize your cluster for specialized workloads:

```bash
# Configure SR-IOV Network Operator
# Set up node tuning for high-performance workloads
```

👉 **Complete Guide**: [Performance Tuning](../how-to/openstack/performance-tuning.md)

### Access Advanced Features

- **[Availability Zones](../how-to/openstack/az.md)**: Distribute nodepools across OpenStack AZs
- **[Additional Ports](../how-to/openstack/additional-ports.md)**: Configure specialized networking
- **[Performance Tuning](../how-to/openstack/performance-tuning.md)**: SR-IOV and node optimization
- **[Global Pull Secret](../how-to/openstack/global-pull-secret.md)**: Configure global registry access

## Troubleshooting

### Common Issues

??? question "Control plane pods not starting"

    **Symptoms**: Hosted control plane pods remain in pending or error state

    **Solutions**:
    ```bash
    # Check management cluster resources
    oc describe nodes

    # Verify OpenStack connectivity
    openstack server list
    openstack network list

    # Check HyperShift operator logs
    oc logs -n hypershift deployment/operator
    ```

??? question "OpenStack authentication failures"

    **Symptoms**: Cluster creation fails with authentication errors

    **Solutions**:
    ```bash
    # Verify clouds.yaml configuration
    openstack --os-cloud $OS_CLOUD server list

    # Check credentials file path
    ls -la $CLOUDS_YAML

    # Verify CA certificate if using self-signed certs
    openstack --os-cloud $OS_CLOUD --os-cacert $CA_CERT_PATH token issue
    ```

??? question "Worker nodes not joining cluster"

    **Symptoms**: OpenStack VMs created but not appearing as nodes

    **Solutions**:
    ```bash
    # Check OpenStack instances
    openstack server list

    # Verify network connectivity
    openstack network list
    openstack security group list

    # Check CAPI provider logs
    oc logs -n clusters-$CLUSTER_NAME deployment/capi-provider
    ```

??? question "Ingress not working"

    **Symptoms**: Applications not accessible via routes

    **Solutions**:
    ```bash
    # Check ingress service
    oc --kubeconfig $CLUSTER_NAME-kubeconfig get svc -n openshift-ingress

    # Verify floating IP assignment
    openstack floating ip list

    # Check DNS resolution
    nslookup console-openshift-console.apps.$CLUSTER_NAME.$BASE_DOMAIN

    # Verify Octavia load balancer
    openstack loadbalancer list
    ```

### Get Help

- 📚 **OpenStack-specific guides**: [How-to guides](../how-to/openstack/)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)
- 🔗 **OpenStack Docs**: [OpenStack Documentation](https://docs.openstack.org/)

## Cleanup

When you're done experimenting:

```bash
# Delete the hosted cluster
hcp destroy cluster openstack --name $CLUSTER_NAME

# Verify cleanup
oc get hostedcluster -n clusters
oc get nodepools -n clusters

# Clean up floating IPs if manually created
openstack floating ip delete $INGRESS_FLOATING_IP

# Clean up any uploaded RHCOS images
openstack image delete rhcos-4.17.0
```

👉 **Complete Guide**: [Destroy OpenStack Cluster](../how-to/openstack/destroy.md)

!!! note "Resource Cleanup"

    Most OpenStack resources are automatically cleaned up when the hosted cluster is deleted. However, manually created floating IPs and uploaded images may need manual cleanup.

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster on OpenStack. This platform provides excellent flexibility for private cloud deployments and development environments.