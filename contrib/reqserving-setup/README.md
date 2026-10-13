# Request Serving Setup Utility

This utility configures an OpenShift management cluster for HyperShift's dedicated request serving isolation. It is based on the setup logic used in the HyperShift e2e test suite.

## Building

To build the utility, run `make` from this directory:

```bash
make
```

This will produce a `reqserving-setup` binary.

## Running

The utility requires a configured `KUBECONFIG` with cluster-admin permissions on the management cluster.

### Apply Configuration

To apply the configuration directly to the cluster:

```bash
./reqserving-setup
```

### Dry Run

To see what resources would be created without applying them, use the `--dry-run-dir` flag:

```bash
./reqserving-setup --dry-run-dir ./manifests
```

## What This Utility Does

The utility performs the following configuration steps:

1.  **Cluster Autoscaler**: Configures the `default` `ClusterAutoscaler` resource to enable `balanceSimilarNodeGroups` and scale-down.
2.  **Machine Health Check**: Creates a `MachineHealthCheck` named `request-serving-mhc` in the `openshift-machine-api` namespace. It monitors nodes labeled with `hypershift.openshift.io/control-plane=true`.
3.  **Common MachineSets**: Creates "common" `MachineSet` resources in 3 availability zones for non-request-serving control plane workloads.
    *   **Taints**: `hypershift.openshift.io/control-plane=true:NoSchedule`
    *   **Autoscaling**: Creates a `MachineAutoscaler` for each, scaling from 0 to 20.
4.  **Request Serving MachineSets**: Creates a comprehensive set of `MachineSet` resources across 2 availability zones for 3 different node sizes (`small`, `medium`, `large`).
    *   **Isolation**: 20 pairs of MachineSets are created for each size and zone.
    *   **Labels**: Nodes are labeled with `hypershift.openshift.io/request-serving-component=true` and `hypershift.openshift.io/node-size=<size>`.
    *   **Taints**: Nodes are tainted with:
        *   `hypershift.openshift.io/control-plane=true:NoSchedule`
        *   `hypershift.openshift.io/control-plane-serving-component=true:NoSchedule`
    *   **Autoscaling**: Creates a `MachineAutoscaler` for each, scaling from 0 to 1.

## Prerequisites

*   The management cluster must be a standalone Highly Available (HA) cluster.
*   Currently, this utility is specific to AWS-based management clusters as it relies on AWS `MachineSet` provider specifications.
