---
title: NodePool Rollouts
---

# NodePool Rollouts

A NodePool rollout is the process by which existing Nodes are replaced or updated when a change in the NodePool or HostedCluster configuration requires it. Understanding what triggers a rollout and how it is executed helps you plan changes with minimal disruption to your workloads.

## What Triggers a Rollout

There are three independent categories of changes that trigger a rollout. A rollout occurs when any one of them detects a difference between the desired state and the current state.

### OCP Release Version

Changing `NodePool.spec.release.image` triggers a rollout. The controller extracts the OCP version from the release image metadata and compares it against the version currently running on the Nodes. If they differ, a rollout begins.

!!! important

    NodePool version must be compatible with the HostedCluster version. See [Versioning Support](versioning-support.md#hostedcluster-and-nodepool-version-compatibility) for details on the version skew policy.

### Node Configuration

Changes to the following fields alter the configuration hash that the controller tracks. When the hash changes, a rollout is triggered:

- **`NodePool.spec.config`** — ConfigMaps containing any of the supported machine configuration APIs:
    - `MachineConfig`
    - `KubeletConfig`
    - `ContainerRuntimeConfig`
    - `ImageContentSourcePolicy`
    - `ImageDigestMirrorSet`
    - `ClusterImagePolicy`

- **`NodePool.spec.tuningConfig`** — references to `Tuned` resources that the Node Tuning Operator translates into `MachineConfig` objects.

- **`HostedCluster.spec.pullSecret`** — a change in the **name** of the referenced Secret triggers a rollout. Changing the content of the Secret without changing the name does not trigger a rollout.

- **`HostedCluster.spec.additionalTrustBundle`** — same behavior as `pullSecret`: only a change in the referenced ConfigMap **name** triggers a rollout.

- **`HostedCluster.spec.imageContentSources`** — changes to image content source policies managed at the HostedCluster level produce an additional core ignition config that alters the configuration hash.

### HostedCluster Global Configuration

Some fields in `HostedCluster.spec.configuration` affect all Nodes and therefore trigger a rollout across **every NodePool** in the cluster when they change:

- **`proxy`** — cluster-wide proxy settings (`httpProxy`, `httpsProxy`, `noProxy`, `trustedCA`). The controller also computes the full `noProxy` list automatically, adding the cluster, service, and machine network CIDRs, cloud metadata endpoints (e.g. `169.254.169.254` for AWS and Azure), and internal compute domains.

- **`image`** — image registry policies (`allowedRegistriesForImport`, `externalRegistryHostnames`, `additionalTrustedCA`, `registrySources`). Although this configuration is served directly by the ignition server rather than embedded in the node user-data, a change still triggers a rollout so Nodes pick up the new configuration.

!!! note

    Other fields inside `HostedCluster.spec.configuration` such as `oauth`, `apiServer`, `authentication`, `scheduler`, or `ingress` do **not** trigger a NodePool rollout. They are reconciled through other control plane mechanisms.

### Platform-Specific Machine Template

Changes to platform-specific infrastructure fields produce a new machine template, which triggers a rollout. The exact fields depend on the platform:

**AWS:**

| Field | Description |
|-------|-------------|
| `spec.platform.aws.ami` | The AMI ID for the worker instances |
| `spec.platform.aws.instanceType` | EC2 instance type |
| `spec.platform.aws.instanceProfile` | IAM instance profile |
| `spec.platform.aws.subnet` | Subnet configuration |
| `spec.platform.aws.securityGroups` | Security group references |
| `spec.platform.aws.rootVolume` | Root volume type, size, IOPS, encryption |
| `spec.platform.aws.placement` | Tenancy and capacity reservation settings |

!!! note

    `spec.platform.aws.resourceTags` is explicitly **excluded** from rollout triggers. Changing tags alone does not cause Nodes to be replaced.

**Other platforms (Azure, KubeVirt, OpenStack, Agent, PowerVS):**

Any change to the platform-specific machine template spec triggers a rollout. Refer to the [API reference](api.md) for the full list of fields per platform.

## What Does Not Trigger a Rollout

The following fields are propagated in-place to existing Nodes without triggering a rollout:

| Field | Behavior |
|-------|----------|
| `spec.nodeLabels` | Propagated directly to existing Machine objects |
| `spec.taints` | Propagated directly to existing Machine objects |
| `spec.replicas` / `spec.autoScaling` | Only changes the number of Nodes, no replacement |
| `spec.nodeDrainTimeout` | Updated on existing Machines without replacement |
| `spec.nodeVolumeDetachTimeout` | Updated on existing Machines without replacement |
| `spec.management.replace.rollingUpdate` | Changes rollout parameters (maxSurge, maxUnavailable) but does not itself cause a rollout |
| `spec.management.autoRepair` | Toggles MachineHealthCheck without replacing Nodes |

## Upgrade Types

The upgrade type determines **how** Nodes are replaced or updated during a rollout. It is set once at NodePool creation and **cannot be changed** afterward.

### Replace

Replace upgrades create new Node instances with the updated configuration and remove old ones. This is the default and recommended approach for cloud environments where creating and destroying instances is cost-effective.

The replacement process is governed by the `spec.management.replace` field:

#### RollingUpdate Strategy (default)

New Nodes are created before old Nodes are removed, ensuring workload availability during the rollout.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `maxSurge` | `1` | Maximum number of Nodes that can be provisioned above the desired count during the rollout. Can be an absolute number or a percentage. |
| `maxUnavailable` | `0` | Maximum number of Nodes that can be unavailable during the rollout. Can be an absolute number or a percentage. |

With the defaults (`maxSurge=1`, `maxUnavailable=0`), one new Node is created at a time, and old Nodes are only removed after the new Node is ready. This is the safest configuration but also the slowest.

To speed up the rollout, you can increase `maxSurge` (more Nodes created in parallel) or increase `maxUnavailable` (allow removing old Nodes before new ones are ready), at the cost of reduced capacity during the rollout.

!!! important

    `maxSurge` and `maxUnavailable` cannot both be `0`.

#### OnDelete Strategy

Old Nodes are only replaced when they are manually deleted. This gives you full control over the rollout pace and order. Once an old Node is deleted, a new Node with the updated configuration is created to replace it.

### InPlace

InPlace upgrades update the operating system of existing Node instances without creating new ones. This is the recommended approach for environments with high infrastructure constraints, such as bare metal.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `maxUnavailable` | `1` | Maximum number of Nodes that can be unavailable during the in-place update. Can be an absolute number or a percentage. The minimum enforced value is `1`. |

!!! important

    When using InPlace upgrades, platform-specific machine template changes (e.g. instance type, AMI) will **only apply to new Nodes** that are created after the change. Existing Nodes are not affected by platform changes.

## Rollout Lifecycle

When a rollout is triggered, the controller follows this sequence:

1. **Change detection** — the controller compares the desired state (from the NodePool and HostedCluster specs) against the current state tracked in the NodePool status and annotations.

2. **New configuration artifacts** — a new ignition token Secret and user-data Secret are generated with names derived from a hash of the new configuration. The previous token is marked as expired.

3. **New machine template** (if platform fields changed) — a new platform-specific machine template is created. Its name includes a hash of the spec, so any change produces a distinct template.

4. **Rollout execution**:
      - **Replace:** the MachineDeployment is updated with the new user-data Secret, machine template, and version references. CAPI orchestrates Node creation and deletion according to the configured strategy (RollingUpdate or OnDelete).
      - **InPlace:** the MachineSet is updated with the new target configuration. An in-place upgrader applies the changes to existing Nodes, respecting `maxUnavailable`.

5. **Completion** — the rollout is considered complete when:
      - **Replace:** all desired replicas are updated and available.
      - **InPlace:** all Nodes report the target configuration version.

6. **Status update** — `NodePool.status.version` is updated and the internal tracking annotations are set to the new values.

### Monitoring Rollout Progress

You can monitor rollout progress through the following NodePool conditions:

| Condition | Meaning |
|-----------|---------|
| `UpdatingVersion` | A version rollout is in progress |
| `UpdatingConfig` | A configuration rollout is in progress |
| `UpdatingPlatformMachineTemplate` | A platform machine template rollout is in progress |

These conditions are set to `True` while the corresponding rollout is in progress and are cleared when it completes.

## Summary Table

| Change | Triggers Rollout | Affects |
|--------|:---:|---------|
| `NodePool.spec.release.image` | Yes | The changed NodePool |
| `NodePool.spec.config` | Yes | The changed NodePool |
| `NodePool.spec.tuningConfig` | Yes | The changed NodePool |
| `HostedCluster.spec.pullSecret` (name change) | Yes | All NodePools |
| `HostedCluster.spec.additionalTrustBundle` (name change) | Yes | All NodePools |
| `HostedCluster.spec.imageContentSources` | Yes | All NodePools |
| `HostedCluster.spec.configuration.proxy` | Yes | All NodePools |
| `HostedCluster.spec.configuration.image` | Yes | All NodePools |
| Platform machine template fields | Yes | The changed NodePool |
| `NodePool.spec.nodeLabels` | No | Propagated in-place |
| `NodePool.spec.taints` | No | Propagated in-place |
| `NodePool.spec.replicas` / `autoScaling` | No | Scale only |
| `NodePool.spec.nodeDrainTimeout` | No | Propagated in-place |
| `NodePool.spec.management.autoRepair` | No | MachineHealthCheck toggle |
| AWS `spec.platform.aws.resourceTags` | No | Applied without rollout |
