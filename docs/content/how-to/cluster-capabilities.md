# Cluster Capabilities

HyperShift allows administrators to enable or disable optional OpenShift components at cluster creation time through the **capabilities** API. This reduces resource consumption and attack surface by preventing unnecessary operators and operands from being deployed.

## Default Behavior

When `spec.capabilities` is not set on a HostedCluster, the cluster uses the OpenShift version's `DefaultCapabilitySet` with the `baremetal` capability excluded. This means most optional components are enabled by default.

!!! important
    Capabilities are **immutable** after cluster creation. They cannot be changed once the HostedCluster is created.

## Supported Capabilities

| Capability | Description |
|---|---|
| `ImageRegistry` | OpenShift Image Registry operator and its operands, including cloud storage infrastructure (S3 buckets, IAM users, etc.) |
| `openshift-samples` | OpenShift Samples operator, which manages example ImageStreams and Templates |
| `Insights` | Insights operator, which collects and uploads cluster telemetry data |
| `baremetal` | Bare metal infrastructure operator. Excluded from the default set; must be explicitly enabled if needed |
| `Console` | OpenShift web console operator and its operands |
| `NodeTuning` | Node Tuning Operator (NTO), which manages node-level performance tuning via TuneD and PerformanceProfiles |
| `Ingress` | OpenShift Ingress operator, which manages the cluster's default router |

## Constraints

The following rules apply when combining capability settings:

- **No overlap**: A capability cannot appear in both `enabled` and `disabled` lists simultaneously.
- **Console requires Ingress**: The `Ingress` capability can only be disabled if `Console` is also disabled, because the console depends on Ingress ([OCPBUGS-58422](https://issues.redhat.com/browse/OCPBUGS-58422)).
- **Version requirement**: Disabling `openshift-samples`, `Insights`, `Console`, `NodeTuning`, or `Ingress` requires OpenShift 4.20 or later. `ImageRegistry` and `baremetal` can be disabled on earlier versions.
- **Baremetal default exclusion**: The `baremetal` capability is already excluded from the default set. Explicitly disabling it is a no-op; explicitly enabling it will add it to the cluster.

## Usage

### CLI

Use the `--disable-cluster-capabilities` flag when creating a cluster:

```shell
hypershift create cluster aws \
  --name my-cluster \
  --disable-cluster-capabilities=ImageRegistry,Console,Ingress
```

Multiple capabilities can be specified as a comma-separated list. The supported values are: `ImageRegistry`, `openshift-samples`, `Insights`, `baremetal`, `Console`, `NodeTuning`, `Ingress`.

### HostedCluster Manifest

To disable capabilities via the HostedCluster resource directly, set `spec.capabilities.disabled`:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  capabilities:
    disabled:
      - ImageRegistry
      - Console
      - Ingress
  # ... rest of spec
```

To explicitly enable a capability that is not in the default set (e.g., `baremetal`):

```yaml
spec:
  capabilities:
    enabled:
      - baremetal
```

Both `enabled` and `disabled` can be used together, as long as no capability appears in both lists:

```yaml
spec:
  capabilities:
    enabled:
      - baremetal
    disabled:
      - ImageRegistry
      - openshift-samples
```
