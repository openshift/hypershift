# Image Registry Mirrors

This document describes how image registry mirrors are handled in HyperShift for both management and guest clusters.

## Overview

HyperShift supports three types of image registry mirror configurations:

1. **ImageContentSourcePolicy (ICSP)** - Deprecated in OpenShift 4.17
2. **ImageDigestMirrorSet (IDMS)** - For digest-based image references (`@sha256:...`)
3. **ImageTagMirrorSet (ITMS)** - For tag-based image references (`:latest`, `:v1.0`, etc.)

## Management Cluster Mirrors

HyperShift automatically detects and uses registry mirrors configured on the management cluster. This affects how release images and other container images are pulled during hosted cluster creation and management.

### Capability Detection

HyperShift detects which mirror types are available on the management cluster:

- `CapabilityICSP` - ImageContentSourcePolicy
- `CapabilityIDMS` - ImageDigestMirrorSet  
- `CapabilityITMS` - ImageTagMirrorSet (new)

The detection happens automatically during initialization and reconciliation cycles.

### Mirror Usage

Mirrors from the management cluster are used for:

- Pulling release images for hosted control planes
- Fetching component images during cluster creation
- Image metadata lookups

All three mirror types (ICSP, IDMS, ITMS) are collected and merged into a unified registry override map.

## Guest Cluster Mirrors

### ImageDigestMirrorSet (Automatic)

ImageDigestMirrorSet is automatically created in guest clusters from `HostedCluster.spec.imageContentSources`:

    apiVersion: hypershift.openshift.io/v1beta1
    kind: HostedCluster
    metadata:
      name: my-cluster
    spec:
      imageContentSources:
      - source: registry.redhat.io
        mirrors:
        - mirror.example.com/redhat

This configuration:
- Automatically creates an IDMS resource in the guest cluster
- The Machine Config Operator (MCO) applies it to worker nodes
- Affects digest-based image pulls on worker nodes

**Important**: `imageContentSources` only creates IDMS, not ITMS.

### ImageTagMirrorSet

ImageTagMirrorSet can be configured using two approaches:

#### Option 1: Cluster-wide via HostedCluster.spec.imageMirrorConfigRef (Recommended)

Configure ITMS at the HostedCluster level for cluster-wide application:

    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: cluster-mirrors
      namespace: clusters
    data:
      itms.yaml: |
        apiVersion: config.openshift.io/v1
        kind: ImageTagMirrorSet
        metadata:
          name: cluster-tag-mirrors
        spec:
          imageTagMirrors:
          - source: quay.io/openshift-release-dev
            mirrors:
            - mirror.corp.com/openshift-release-dev
            mirrorSourcePolicy: AllowContactingSource
    ---
    apiVersion: hypershift.openshift.io/v1beta1
    kind: HostedCluster
    metadata:
      name: my-cluster
      namespace: clusters
    spec:
      imageMirrorConfigRef:
        name: cluster-mirrors
      # ...other configuration

This configuration:
- Applies ITMS to **all NodePools** in the cluster automatically
- ConfigMap must contain key `itms.yaml` with ITMS configuration
- Can also contain `idms.yaml` key for unified IDMS+ITMS configuration
- Parsed and reconciled by the HostedControlPlane controller
- Changes trigger automatic NodePool rollouts
- Recommended for fleet management and GitOps workflows

**Note**: The ConfigMap can contain both `idms.yaml` and `itms.yaml` keys for unified mirror configuration. See the "Unified Configuration" section below.

#### Option 2: Per-NodePool via NodePool.spec.config

Configure ITMS for specific NodePools:

    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: nodepool-tag-mirrors
      namespace: clusters
    data:
      config: |
        apiVersion: config.openshift.io/v1
        kind: ImageTagMirrorSet
        metadata:
          name: nodepool-tag-mirrors
        spec:
          imageTagMirrors:
          - source: quay.io/openshift-release-dev
            mirrors:
            - mirror.corp.com/openshift-release-dev
            mirrorSourcePolicy: AllowContactingSource
    ---
    apiVersion: hypershift.openshift.io/v1beta1
    kind: NodePool
    metadata:
      name: workers
      namespace: clusters
    spec:
      config:
      - name: nodepool-tag-mirrors
      # ...other configuration

This configuration:
- Applies only to the **specific NodePool** referencing it
- ConfigMap must contain key `config` with ITMS configuration
- Validated by the NodePool controller
- Applied to worker nodes via MCO
- Useful for NodePool-specific mirror requirements

#### Comparison: Cluster-wide vs Per-NodePool

| Aspect | imageMirrorConfigRef | NodePool.spec.config |
|--------|---------------------|---------------------|
| **Scope** | All NodePools in cluster | Single NodePool only |
| **Configuration location** | HostedCluster.spec | NodePool.spec |
| **ConfigMap key** | `itms.yaml` (and optionally `idms.yaml`) | `config` |
| **Reconciliation** | HostedControlPlane controller | NodePool controller |
| **Rollout trigger** | Automatic for all NodePools | Only for configured NodePool |
| **Use case** | Fleet-wide policies, disconnected environments | NodePool-specific requirements |
| **GitOps-friendly** | ✅ Highly (single source of truth) | ⚠️ Requires per-NodePool config |

### Unified Configuration (IDMS + ITMS)

For comprehensive mirror coverage, use `HostedCluster.spec.imageMirrorConfigRef` with both IDMS and ITMS in a single ConfigMap:

    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: unified-mirrors
      namespace: clusters
    data:
      idms.yaml: |
        apiVersion: config.openshift.io/v1
        kind: ImageDigestMirrorSet
        metadata:
          name: cluster-digest-mirrors
        spec:
          imageDigestMirrors:
          - source: registry.redhat.io
            mirrors:
            - mirror.corp.com/redhat
      itms.yaml: |
        apiVersion: config.openshift.io/v1
        kind: ImageTagMirrorSet
        metadata:
          name: cluster-tag-mirrors
        spec:
          imageTagMirrors:
          - source: quay.io/openshift-release-dev
            mirrors:
            - mirror.corp.com/openshift-release-dev

This approach:
- Provides single source of truth for all mirror configurations
- Automatically merges with `imageContentSources` if both are specified
- Simplifies fleet management across hundreds of clusters
- Ideal for GitOps workflows and disconnected environments

## Comparison: IDMS vs ITMS

| Aspect | IDMS | ITMS |
|--------|------|------|
| **Auto-creation** | ✅ Yes (from imageContentSources) | ❌ No (explicit config required) |
| **Cluster-wide config** | ✅ imageMirrorConfigRef or imageContentSources | ✅ imageMirrorConfigRef |
| **Per-NodePool config** | ✅ Yes (via NodePool.spec.config) | ✅ Yes (via NodePool.spec.config) |
| **Management cluster detection** | ✅ Yes | ✅ Yes |
| **Image reference type** | Digest (@sha256:...) | Tag (:latest, :v1.0) |
| **API version** | config.openshift.io/v1 | config.openshift.io/v1 |
| **Short name** | idms | itms |

## Supported Configurations in NodePool

The following configuration types are supported in `NodePool.spec.config`:

- `MachineConfig`
- `KubeletConfig`
- `ContainerRuntimeConfig`
- `ImageContentSourcePolicy` (deprecated)
- `ImageDigestMirrorSet`
- `ImageTagMirrorSet` (new)
- `ClusterImagePolicy`

All are validated to ensure they are MCO-consumable resources.

## Implementation Details

### Workflows

#### Management Cluster Mirror Detection
    Management cluster has ITMS CRs
        ↓
    CapabilityITMS detected
        ↓
    getImageTagMirrorSets() reads ITMS
        ↓
    GetAllImageRegistryMirrors() collects all mirrors (ICSP + IDMS + ITMS)
        ↓
    Used for release image pulls from management cluster

#### Guest Cluster IDMS (Automatic)
    HostedCluster.spec.imageContentSources
        ↓
    HostedControlPlane.spec.imageContentSources
        ↓
    reconcileImageContentPolicyType()
        ↓
    Deletes deprecated ICSP
        ↓
    Creates/updates IDMS only

#### Guest Cluster ITMS (Cluster-wide or Per-NodePool)
    Option A: HostedCluster.spec.imageMirrorConfigRef
           ↓
    HostedControlPlane parses itms.yaml
           ↓
    Reconciles ITMS for all NodePools
           ↓
    MCO deploys ITMS to nodes
           ↓
    registries.conf contains tag-based mirrors
    
    Option B: NodePool.spec.config[] references ConfigMap
           ↓
    NodePool controller validates ITMS
           ↓
    MCO deploys ITMS to nodes
           ↓
    registries.conf contains tag-based mirrors

## Migration from ICSP

ImageContentSourcePolicy (ICSP) is deprecated and will be removed in OpenShift 4.17. HyperShift automatically:

1. Deletes any existing ICSP resources
2. Creates IDMS from `imageContentSources`
3. Continues to support ICSP on management clusters for backwards compatibility

For tag-based mirrors previously configured with ICSP, users should:

1. Create an ImageTagMirrorSet ConfigMap
2. Reference it in NodePool.spec.config
3. Remove old ICSP-based configurations

## Best Practices

### For Digest-based Mirrors
Use `HostedCluster.spec.imageContentSources`:

    spec:
      imageContentSources:
      - source: registry.redhat.io
        mirrors:
        - mirror.example.com/redhat

### For Tag-based Mirrors
The recommended approach is to use `HostedCluster.spec.imageMirrorConfigRef` for a unified cluster-wide configuration. For per-nodepool overrides, `NodePool.spec.config` can be used as an alternative. For comprehensive details, refer to the unified configuration approach described in the how-to guide.

### For Management Clusters
Configure both IDMS and ITMS at the management cluster level for comprehensive mirror coverage.

## Compatibility

- **Minimum OpenShift Version**: 4.13+ (when ITMS API is available)
- **Backwards Compatible**: Yes - ICSP and IDMS continue to work
- **Management Cluster**: Automatically detects available mirror types

## References

- [OpenShift Image Configuration](https://docs.openshift.com/container-platform/4.13/openshift_images/image-configuration.html)
- [JIRA OCPNODE-1258](https://issues.redhat.com/browse/OCPNODE-1258) - ICSP Deprecation
- [containers/image registries.conf](https://github.com/containers/image/blob/main/docs/containers-registries.conf.5.md)
