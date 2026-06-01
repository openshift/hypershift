# Autoscaling for Self-Managed Azure HostedClusters

This guide covers configuring autoscaling for self-managed Azure HostedClusters. For the full autoscaling configuration reference, see the [Autoscaling](../autoscaling.md) guide.

## Prerequisites

- A self-managed Azure HostedCluster created following the [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md) guide
- `oc` or `kubectl` CLI with access to the management cluster

## Example: Full Autoscaling Configuration

This example configures a HostedCluster with two Azure NodePools balanced by the autoscaler:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  autoscaling:
    scaling: ScaleUpAndScaleDown
    maxNodesTotal: 12
    expanders:
      - Random
    scaleDown:
      delayAfterAddSeconds: 300
      unneededDurationSeconds: 600
      utilizationThresholdPercent: 50
    balancingIgnoredLabels:
      - "custom.label/environment"
    maxFreeDifferenceRatioPercent: 70
---
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: my-cluster-nodepool-1
  namespace: clusters
spec:
  clusterName: my-cluster
  autoScaling:
    min: 1
    max: 6
  platform:
    azure:
      vmSize: Standard_D4s_v3
      # ... other required fields (image, osDisk) omitted for brevity
---
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: my-cluster-nodepool-2
  namespace: clusters
spec:
  clusterName: my-cluster
  autoScaling:
    min: 1
    max: 6
  platform:
    azure:
      vmSize: Standard_D4s_v3
      # ... other required fields (image, osDisk) omitted for brevity
```

## Azure-Specific Details

- HyperShift provisions individual Azure Virtual Machines via CAPZ when scaling up NodePools.
- Scale-from-zero (`autoScaling.min: 0`) is not supported on Azure. The minimum must be >= 1.
