---
title: Dual-Stream RHEL OS Support
---

# Dual-Stream RHEL OS Support

Starting with OCP 5.0, HyperShift NodePools support dual-stream RHEL OS provisioning. Cluster administrators can choose between **RHEL 9** and **RHEL 10** OS images for worker nodes on a per-NodePool basis using the `spec.osImageStream` field.

!!! note "Feature Gate"

    This feature requires the `OSStreams` feature gate to be enabled.

## How It Works

Each NodePool resolves an OS stream based on three inputs:

1. **Explicit selection** (`spec.osImageStream.name`): The user can explicitly set `rhel-9` or `rhel-10`.
2. **OCP release version**: The release image version determines which streams are available.
3. **Container runtime**: If a `ContainerRuntimeConfig` using `runc` is attached to the NodePool, RHEL 10 is unavailable because RHEL 10 does not include `runc`.

When the stream is resolved, the NodePool controller generates an `OSImageStream` custom resource in the ignition payload, which the Machine Config Operator uses to select the correct OS and extension images.

## OS Stream Resolution Matrix

The following table describes the resolved OS stream for every combination of user selection, release version, and container runtime:

| `spec.osImageStream.name` | OCP Version | Uses runc | Resolved Stream | Outcome |
|---|---|---|---|---|
| *(unset)* | < 5.0 | Any | *(none)* | Legacy behavior — default RHCOS images are used. |
| *(unset)* | ≥ 5.0 | No | `rhel-10` | Default for OCP 5.0+. |
| *(unset)* | ≥ 5.0 | Yes | `rhel-9` | Automatic fallback — a condition message surfaces the reason. |
| `rhel-9` | Any | Any | `rhel-9` | Explicit selection, always valid. |
| `rhel-10` | < 5.0 | Any | **Error** | RHEL 10 is not supported on releases before 5.0. |
| `rhel-10` | ≥ 5.0 | Yes | **Error** | RHEL 10 does not support `runc`. |
| `rhel-10` | ≥ 5.0 | No | `rhel-10` | Explicit selection, valid. |

## Selecting an OS Stream

To explicitly select an OS stream for a NodePool, set `spec.osImageStream.name`:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: my-nodepool
  namespace: clusters
spec:
  release:
    image: quay.io/openshift-release-dev/ocp-release:5.0.0-x86_64
  osImageStream:
    name: rhel-10
  # ...other fields
```

When omitted, the stream is resolved automatically based on the release version and runtime configuration.

## Stream Transitions

Changing `spec.osImageStream.name` triggers a NodePool rollout (Replace or InPlace, depending on `spec.management.upgradeType`).

**Allowed transitions:**

- *(unset)* → `rhel-9` or `rhel-10`
- `rhel-9` → `rhel-10` *(forward upgrade)*

**Blocked transitions:**

- `rhel-10` → `rhel-9` *(downgrade)* — rejected by API validation. To move back to RHEL 9, create a new NodePool.

!!! warning

    OS stream downgrades from `rhel-10` to `rhel-9` are not supported because in-place OS major version downgrades are not safe. If you need RHEL 9 nodes after upgrading to RHEL 10, create a new NodePool with `spec.osImageStream.name: rhel-9` and migrate workloads.

## runc Compatibility

RHEL 10 removes support for the `runc` container runtime. If a NodePool references a `ContainerRuntimeConfig` that sets `runc` as the default runtime:

- **Explicit `rhel-10` selection**: The NodePool controller rejects the configuration and sets a `ValidMachineConfig=False` condition with the message: *"OS stream rhel-10 is not compatible with ContainerRuntimeConfig using runc: RHEL 10 does not support runc"*.
- **No explicit stream on OCP ≥ 5.0**: The controller automatically falls back to `rhel-9` and surfaces a condition message: *"OS stream defaulted to rhel-9: NodePool uses runc ContainerRuntimeConfig which is not supported on rhel-10"*.

To use RHEL 10, remove or update the `ContainerRuntimeConfig` to use `crun` (the default on RHEL 10).

## Observability

### Status Fields

The resolved OS stream observed on running nodes is reported in `status.osImageStream`:

```yaml
status:
  osImageStream:
    name: rhel-10
```

This field is inferred from the CAPI Machine `NodeInfo.OSImage` field of registered nodes. It reflects what is actually running, not what was requested in the spec.

### Conditions

OS stream validation errors and informational messages appear on the `ValidMachineConfig` condition:

| Scenario | Condition Status | Message |
|---|---|---|
| Valid configuration | `True` | *(empty or standard)* |
| Invalid stream for release | `False` | `invalid OS stream configuration: OS stream "rhel-10" is not supported for release version 4.x (requires >= 5.0.0)` |
| runc + rhel-10 conflict | `False` | `invalid OS stream configuration: OS stream "rhel-10" is not compatible with ContainerRuntimeConfig using runc` |
| Implicit rhel-9 fallback | `True` | `OS stream defaulted to rhel-9: NodePool uses runc ContainerRuntimeConfig which is not supported on rhel-10` |

## Common Scenarios

### Upgrading from OCP 4.x to 5.0

When upgrading a HostedCluster from OCP 4.x to 5.0+, NodePools with no explicit `osImageStream` will automatically transition from legacy RHCOS images to `rhel-10` (or `rhel-9` if runc is in use). This transition is handled as part of the normal NodePool rollout triggered by the release image change.

### Mixed OS Pools in a Cluster

You can run multiple NodePools with different OS streams in the same HostedCluster. For example:

- `nodepool-default`: `osImageStream.name: rhel-10` — for general workloads
- `nodepool-legacy`: `osImageStream.name: rhel-9` — for workloads that require runc or RHEL 9 compatibility

### Migrating from RHEL 9 to RHEL 10

1. Verify the HostedCluster is running OCP ≥ 5.0.
2. Ensure no `ContainerRuntimeConfig` with runc is attached to the NodePool.
3. Set `spec.osImageStream.name: rhel-10` on the NodePool.
4. Monitor the rollout via `status.conditions` and `status.osImageStream`.
