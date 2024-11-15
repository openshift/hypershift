# Performance guide

This document describes how to tune the performance of nodes in a HostedCluster on OpenStack

## Use-cases

- **CNF**: Cloud-native network functions (CNFs) are network functions that are designed to run in cloud-native environments. CNFs can be used to provide network services such as routing, firewalling, and load balancing. The Nodepool can be configured to use high-performance computing and networking devices to run CNFs.

## Prerequisite

- An OpenStack flavor with the necessary resources to run the workload: dedicated CPU, memory, and host aggregate
  information so the instance will be scheduled on a host with capable hardware (e.g. Intel or Mellanox NICs capable
  of SR-IOV, DPDK, etc).
- An OpenStack network attached to the SR-IOV or DPDK capable NICs; this network must be usable by the project
  used by the HostedCluster.

## Node tuning

Before creating an additional Nodepool used for performance workloads, we'll create a PerformanceProfile that will be
used for the nodes.

The PerformanceProfile is a custom resource that allows you to define the performance characteristics of a node. The
PerformanceProfile can be used to configure the CPU, memory, hugepages, real-time kernel, and other performance-related
settings of a node.

Here is an example of a PerformanceProfile that can be used to configure a node for high-performance workloads:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: perfprof-1
  namespace: clusters
data:
  tuning: |
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: cnf-performanceprofile
      namespace: "${HYPERSHIFT_NAMESPACE}"
    data:
      tuning: |
        apiVersion: performance.openshift.io/v2
        kind: PerformanceProfile
        metadata:
          name: cnf-performanceprofile
        spec:
          additionalKernelArgs:
            - nmi_watchdog=0
            - audit=0
            - mce=off
            - processor.max_cstate=1
            - idle=poll
            - intel_idle.max_cstate=0
            - amd_iommu=on
          cpu:
            isolated: "${CPU_ISOLATED}"
            reserved: "${CPU_RESERVED}"
          hugepages:
            defaultHugepagesSize: "1G"
            pages:
              - count: ${HUGEPAGES}
                node: 0
                size: 1G
          nodeSelector:
            node-role.kubernetes.io/worker: ''
          realTimeKernel:
            enabled: false
          globallyDisableIrqLoadBalancing: true
```

Create this PerformanceProfile (change the namespace field in the example above to specify the clusters namespace) by running:

```shell
oc apply -f perfprof.yaml
```

## Nodepool creation

After creating the PerformanceProfile, we can create a Nodepool that will use the PerformanceProfile for the nodes.

```shell
export NODEPOOL_NAME=$CLUSTER_NAME-cnf
export FLAVOR="m1.xlarge.nfv"

hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count 0 \
  --openstack-node-flavor $FLAVOR
```

Now let's patch the Nodepool to use the PerformanceProfile:

```shell
oc patch nodepool -n ${HYPERSHIFT_NAMESPACE} ${CLUSTER_NAME} -p '{"spec":{"tuningConfig":[{"name":"cnf-performanceprofile"}]}}' --type=merge
```

We can scale the Nodepool to the desired number of nodes:

```shell
oc scale NodePool/$CLUSTER_NAME --namespace ${HYPERSHIFT_NAMESPACE} --replicas=1
```

To wait for the node to be ready:

```shell
oc wait --for=condition=UpdatingConfig=True nodepool -n ${HYPERSHIFT_NAMESPACE} ${CLUSTER_NAME} --timeout=5m
oc wait --for=condition=UpdatingConfig=False nodepool -n ${HYPERSHIFT_NAMESPACE} ${CLUSTER_NAME} --timeout=30m
oc wait --for=condition=AllNodesHealthy nodepool -n ${HYPERSHIFT_NAMESPACE} ${CLUSTER_NAME} --timeout=5m
```

Once the nodes have been deployed, you can SSH into the nodes and check the performance settings:

```shell
ssh core@<node-ip>
```
chroot /host
cat /proc/cmdline
```

## SR-IOV Network Operator

The SR-IOV Network Operator is a Kubernetes operator that manages SR-IOV devices on the nodes where workloads will be running. In the context of OpenShift on OpenStack, the operator can be used to create SR-IOV network attachments that can be used by workload pods.

The nodes deployed by the Nodepool must be labeled so the SR-IOV Network Operator can be deployed on these nodes.

If you haven't done it already, we need to create a kubeconfig file for the cluster and export it:
```shell
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
export KUBECONFIG=$CLUSTER_NAME-kubeconfig
```

For every node that was deployed by the Nodepool, label it with the following command:

```shell
oc label node "${WORKER_NODE}" feature.node.kubernetes.io/network-sriov.capable="true"
```

The operator can now be installed in the guest cluster by following the [SR-IOV Network Operator documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.17/html-single/networking/index#installing-sr-iov-operator_installing-sriov-operator).
The rest of the operations in order to get the workload running with high performances are the same as on a Standalone OCP cluster.
