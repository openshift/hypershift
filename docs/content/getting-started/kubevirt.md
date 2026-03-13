---
title: Getting Started with KubeVirt
description: Deploy your first HyperShift hosted cluster on virtualized infrastructure
---

# Getting Started with HyperShift on KubeVirt

KubeVirt provides a unique approach to hosting OpenShift clusters by running them as virtual machines within an existing OpenShift cluster. This platform is excellent for development, testing, and scenarios where you need nested virtualization capabilities.

## Choose Your Deployment Model

=== "KubeVirt (Self-Managed Only)"

    **Virtualized Infrastructure with HyperShift**

    - ✅ **Nested Virtualization** - Clusters run as VMs on existing OpenShift
    - ✅ **Development Friendly** - Perfect for testing and development environments
    - ✅ **Resource Efficient** - Share compute resources across multiple clusters
    - ✅ **Quick Setup** - Fast cluster provisioning on existing infrastructure
    - 🛠️ **Self-Managed** - You control the management cluster and infrastructure
    - ⚠️ **Performance Overhead** - Virtual machine layer affects performance

    Perfect for: Development environments, testing, proof-of-concepts, multi-tenancy scenarios

!!! note "Single Deployment Option"

    KubeVirt only supports self-managed HyperShift deployments. The virtual machines run on your existing OpenShift cluster, which serves as the management platform.

## KubeVirt Quickstart

### Prerequisites

Before you begin, ensure your management OpenShift cluster has:

- [x] **OpenShift cluster** (4.14+) with admin access
- [x] **OpenShift Virtualization** (4.14+) installed and configured
- [x] **OVN-Kubernetes** as the default pod network CNI
- [x] **LoadBalancer support** (e.g., MetalLB installed)
- [x] **Default storage class** configured
- [x] **Wildcard DNS routes** enabled
- [x] **Pull secret** from [Red Hat Console](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned)

### Step 1: Prepare Your Management Cluster

Configure the required cluster settings:

```bash
# Enable wildcard DNS routes
oc patch ingresscontroller -n openshift-ingress-operator default \
  --type=json \
  -p '[{"op": "add", "path": "/spec/routeAdmission", "value": {"wildcardPolicy": "WildcardsAllowed"}}]'

# Verify OpenShift Virtualization is installed
oc get csv -n openshift-cnv

# Verify LoadBalancer support (MetalLB example)
oc get pods -n metallb-system

# Check default storage class
oc get storageclass
```

!!! tip "Storage Class Configuration"

    If no default storage class exists, set one:
    ```bash
    # Example with OCS/ODF storage
    oc patch storageclass ocs-storagecluster-ceph-rbd \
      -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
    ```

!!! tip "Network Performance"

    For optimal performance, ensure your management cluster has an MTU of 9000 or larger. Smaller MTU settings will work but may impact network performance.

### Step 2: Install HyperShift CLI Tools

Build or download the HyperShift and HCP CLI tools:

!!! important "CLI Tool Support"

    - **`hcp` CLI**: Officially supported for production use and cluster management
    - **`hypershift` CLI**: Developer-only tool, not supported for production. Used primarily for operator installation and development workflows

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

### Step 3: Install HyperShift Operator

Deploy the HyperShift operator on your management cluster:

```bash
# Install HyperShift operator
hypershift install

# Verify installation
oc get pods -n hypershift

# Example output:
# NAME                        READY   STATUS    RESTARTS   AGE
# operator-755d587f44-lrtrq   1/1     Running   0          114s
# operator-755d587f44-qj6pz   1/1     Running   0          114s
```

### Step 4: Create Your First Hosted Cluster

Configure and create your KubeVirt hosted cluster:

```bash
# Set cluster configuration
export CLUSTER_NAME="my-kubevirt-cluster"
export PULL_SECRET="$HOME/pull-secret.json"
export MEM="6Gi"           # Memory per worker VM
export CPU="2"             # CPUs per worker VM
export WORKER_COUNT="2"    # Number of worker nodes

# Create the hosted cluster
hcp create cluster kubevirt \
  --name $CLUSTER_NAME \
  --node-pool-replicas $WORKER_COUNT \
  --pull-secret $PULL_SECRET \
  --memory $MEM \
  --cores $CPU
```

!!! note "Resource Requirements"

    Each worker VM will consume the specified memory and CPU resources from your management cluster. Ensure your cluster has sufficient capacity.

### Step 5: Monitor Cluster Creation

```bash
# Monitor cluster creation progress (takes 10-15 minutes)
oc get hostedcluster -n clusters -w

# Check control plane pods
oc get pods -n clusters-$CLUSTER_NAME

# Check when cluster is ready
oc get hostedcluster $CLUSTER_NAME -n clusters

# Example ready state:
# NAME      VERSION   KUBECONFIG                PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
# example   4.14.0    example-admin-kubeconfig  Completed  True        False         The hosted control plane is available
```

### Step 6: Access Your Hosted Cluster

```bash
# Get cluster kubeconfig
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig

# Access your hosted cluster
export KUBECONFIG=$CLUSTER_NAME-kubeconfig

# Verify access
oc get nodes
oc get clusterversion

# Example output:
# NAME                  STATUS   ROLES    AGE   VERSION
# example-n6prw         Ready    worker   32m   v1.27.4+18eadca
# example-nc6g4         Ready    worker   32m   v1.27.4+18eadca
```

🎉 **Your KubeVirt hosted cluster is ready!** You now have a fully functional OpenShift cluster running as VMs.

## Advanced Configuration

### VM Scheduling and Placement

Control where your VMs are scheduled:

```bash
# Create cluster with node selector for VM placement
hcp create cluster kubevirt \
  --name $CLUSTER_NAME \
  --node-pool-replicas $WORKER_COUNT \
  --pull-secret $PULL_SECRET \
  --memory $MEM \
  --cores $CPU \
  --vm-node-selector "node-role.kubernetes.io/worker=,zone=east"
```

### Configure VM Distribution

Install and configure the De-Scheduler for better VM distribution:

```yaml
# Apply this configuration to enable VM redistribution
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
  namespace: openshift-kube-descheduler-operator
spec:
  mode: Automatic
  managementState: Managed
  deschedulingIntervalSeconds: 60
  profiles:
  - SoftTopologyAndDuplicates
  - EvictPodsWithPVC
  - EvictPodsWithLocalStorage
  profileCustomizations:
    devEnableEvictionsInBackground: true
```

### Create Additional Node Pools

Add specialized node pools with different configurations:

```bash
# Create a high-CPU node pool
export NODEPOOL_NAME="$CLUSTER_NAME-high-cpu"
export WORKER_COUNT="2"
export MEM="8Gi"
export CPU="4"
export DISK="32"  # Root volume size in GB

hcp create nodepool kubevirt \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --replicas $WORKER_COUNT \
  --memory $MEM \
  --cores $CPU \
  --root-volume-size $DISK
```

### Scale Your Cluster

```bash
# Scale existing node pool
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
oc new-app --image=quay.io/quay/busybox:latest --name=vm-test
oc get pods -w
```

### Configure Storage

Configure persistent storage for your applications:

```bash
# Check available storage classes in hosted cluster
oc get storageclass

# Create a PVC
oc create -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
EOF
```

### Access Advanced Features

- **[Storage Configuration](../how-to/kubevirt/configuring-storage.md)**: Configure persistent storage options
- **[Network Configuration](../how-to/kubevirt/configuring-network.md)**: Advanced networking setup
- **[Performance Tuning](../how-to/kubevirt/performance-tuning.md)**: Optimize VM performance
- **[GPU Devices](../how-to/kubevirt/gpu-devices.md)**: Configure GPU passthrough
- **[External Infrastructure](../how-to/kubevirt/external-infrastructure.md)**: Use external infrastructure components

## Troubleshooting

### Common Issues

??? question "VMs not starting"

    **Symptoms**: NodePool VMs remain in pending or error state

    **Solutions**:
    ```bash
    # Check node capacity and resources
    oc describe nodes

    # Check VM events
    oc get vmi -A
    oc describe vmi -n clusters-$CLUSTER_NAME

    # Verify storage class and PVC creation
    oc get pvc -n clusters-$CLUSTER_NAME
    ```

??? question "Network connectivity issues"

    **Symptoms**: Pods cannot reach external services or each other

    **Solutions**:
    ```bash
    # Verify OVN-Kubernetes is running
    oc get pods -n openshift-ovn-kubernetes

    # Check LoadBalancer services
    oc get svc -n clusters-$CLUSTER_NAME

    # Verify ingress controller
    oc get pods -n openshift-ingress-operator
    ```

??? question "Storage provisioning failures"

    **Symptoms**: PVCs remain in pending state

    **Solutions**:
    ```bash
    # Check default storage class
    oc get storageclass

    # Verify storage operator status
    oc get csv -n openshift-storage

    # Check available storage on management cluster nodes
    oc get nodes -o custom-columns=NAME:.metadata.name,STORAGE:.status.capacity.ephemeral-storage
    ```

??? question "OpenShift Virtualization not working"

    **Symptoms**: Cannot create VMs or VirtualMachines

    **Solutions**:
    ```bash
    # Check OpenShift Virtualization installation
    oc get csv -n openshift-cnv

    # Verify kubevirt and CDI operators
    oc get pods -n openshift-cnv

    # Check virtualization features on nodes
    oc get nodes -o json | jq '.items[].status.allocatable'
    ```

### Get Help

- 📚 **KubeVirt-specific guides**: [How-to guides](../how-to/kubevirt/)
- 🔧 **Troubleshooting**: [KubeVirt troubleshooting](../how-to/kubevirt/troubleshooting-kubevirt-cluster.md)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)
- 🔗 **KubeVirt Docs**: [KubeVirt Documentation](https://kubevirt.io/user-guide/)

## Cleanup

When you're done experimenting:

```bash
# Delete the hosted cluster
hcp destroy cluster kubevirt --name $CLUSTER_NAME

# Verify cleanup
oc get hostedcluster -n clusters
oc get vmi -A  # Should show no VMs from deleted cluster
```

!!! tip "Resource Cleanup"

    KubeVirt hosted clusters clean up automatically when deleted. The underlying VMs and storage are removed as part of the deletion process.

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster running on KubeVirt. This platform is perfect for development environments where you need multiple isolated clusters without the overhead of separate physical infrastructure.