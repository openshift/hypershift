# Configure Image Registry Mirrors

This guide shows how to configure image registry mirrors for HyperShift-hosted clusters.

## Prerequisites

- A HyperShift management cluster
- Appropriate permissions to create HostedCluster and NodePool resources

## Overview

HyperShift supports two types of image registry mirrors:

1. **ImageDigestMirrorSet (IDMS)** - For digest-based image pulls (e.g., `registry.io/image@sha256:abc123`)
2. **ImageTagMirrorSet (ITMS)** - For tag-based image pulls (e.g., `registry.io/image:latest`)

You can configure these mirrors using two approaches:

- **HostedCluster API** (recommended for fleet management) - Configure via `imageMirrorConfigRef` for unified IDMS/ITMS configuration
- **Legacy approach** - IDMS via `imageContentSources`, ITMS via NodePool ConfigMap

## Unified Configuration via imageMirrorConfigRef (Recommended)

This approach provides a consistent, GitOps-friendly way to manage both IDMS and ITMS configurations from the HostedCluster level.

### Step 1: Create the Image Mirror ConfigMap

Create a ConfigMap containing both IDMS and ITMS configurations:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: global-image-mirrors
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
        - mirror.example.com/redhat
      - source: quay.io
        mirrors:
        - mirror.example.com/quay
  
  itms.yaml: |
    apiVersion: config.openshift.io/v1
    kind: ImageTagMirrorSet
    metadata:
      name: cluster-tag-mirrors
    spec:
      imageTagMirrors:
      - source: quay.io/openshift-release-dev
        mirrors:
        - mirror.example.com/openshift-release-dev
        mirrorSourcePolicy: AllowContactingSource
      - source: registry.redhat.io/rhel9
        mirrors:
        - mirror.example.com/rhel9
        mirrorSourcePolicy: NeverContactSource
```

Apply the ConfigMap:

```bash
oc apply -f image-mirror-config.yaml
```

### Step 2: Reference in HostedCluster

Update your HostedCluster to reference the ConfigMap:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  imageMirrorConfigRef:
    name: global-image-mirrors
  # ...other hostedcluster configuration
```

Apply the configuration:

```bash
oc apply -f hostedcluster.yaml
```

This approach automatically reconciles both IDMS and ITMS configurations for all NodePools in the cluster.

### Benefits of Unified Configuration

1. **Consistency** - Both IDMS and ITMS follow the same reconciliation flow
2. **GitOps-friendly** - Configurations can be versioned and templated
3. **Fleet scalability** - Single ConfigMap reference scales across hundreds of clusters
4. **Centralized management** - Update mirrors across all clusters by updating the ConfigMap

## Legacy Configuration Approaches

### Configure Digest-based Mirrors (IDMS) via imageContentSources

Digest-based mirrors are automatically created from `imageContentSources` in your HostedCluster:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  imageContentSources:
  - source: registry.redhat.io
    mirrors:
    - mirror.example.com/redhat
  - source: quay.io
    mirrors:
    - mirror.example.com/quay
```

Apply the configuration:

```bash
oc apply -f hostedcluster.yaml
```

This automatically creates an ImageDigestMirrorSet in the guest cluster that affects all digest-based image pulls (e.g., `registry.redhat.io/image@sha256:abc123`).

### Configure Tag-based Mirrors (ITMS) via NodePool ConfigMap

Tag-based mirrors require explicit configuration via ConfigMap:

#### Step 1: Create the ITMS ConfigMap

Create a file `itms-config.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: tag-mirrors
  namespace: clusters
data:
  config: |
    apiVersion: config.openshift.io/v1
    kind: ImageTagMirrorSet
    metadata:
      name: worker-tag-mirrors
    spec:
      imageTagMirrors:
      - source: quay.io/openshift-release-dev
        mirrors:
        - mirror.example.com/openshift-release-dev
        mirrorSourcePolicy: AllowContactingSource
      - source: registry.redhat.io/rhel9
        mirrors:
        - mirror.example.com/rhel9
        mirrorSourcePolicy: NeverContactSource
```

Apply the ConfigMap:

```bash
oc apply -f itms-config.yaml
```

#### Step 2: Reference in NodePool

Update your NodePool to reference the ConfigMap:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: my-cluster-workers
  namespace: clusters
spec:
  clusterName: my-cluster
  replicas: 3
  config:
  - name: tag-mirrors
  # ...other nodepool configuration
```

Apply the NodePool:

```bash
oc apply -f nodepool.yaml
```

## Mirror Source Policies

Both IDMS and ITMS support `mirrorSourcePolicy` to control fallback behavior:

### AllowContactingSource (Default)

Falls back to the original registry if mirrors fail:

```yaml
mirrorSourcePolicy: AllowContactingSource
```
Use this when:
- Mirrors may be temporarily unavailable
- You want high availability
- Network policies allow accessing both mirrors and original registries

### NeverContactSource

Never contacts the original registry, only uses mirrors:

```yaml
mirrorSourcePolicy: NeverContactSource
```

Use this when:
- Running in disconnected environments
- Network policies block access to original registries
- You want to enforce mirror usage

## Verify Configuration

### Check IDMS in Guest Cluster

```bash
# Get hosted cluster kubeconfig
oc extract secret/my-cluster-admin-kubeconfig --to=- > /tmp/my-cluster-kubeconfig

# Check IDMS
oc --kubeconfig=/tmp/my-cluster-kubeconfig get imagedigestmirrorsets
oc --kubeconfig=/tmp/my-cluster-kubeconfig get imagedigestmirrorsets cluster -o yaml
```

### Check ITMS in Guest Cluster

```bash
# Check ITMS
oc --kubeconfig=/tmp/my-cluster-kubeconfig get imagetagmirrorsets
oc --kubeconfig=/tmp/my-cluster-kubeconfig get imagetagmirrorsets worker-tag-mirrors -o yaml
```

### Verify on Worker Nodes

SSH into a worker node and check the registries configuration:

```bash
# View registries.conf
cat /etc/containers/registries.conf.d/99-*

# Or check the merged configuration
cat /etc/containers/registries.conf
```

You should see both digest and tag mirrors configured.

## Troubleshooting

### ITMS Not Applied

If ITMS is not showing up in the guest cluster:

1. Check if the ConfigMap exists:

```bash
oc get configmap tag-mirrors -n clusters -o yaml
```

2. Verify NodePool references the ConfigMap:

```bash
oc get nodepool my-cluster-workers -n clusters -o yaml | grep -A5 config
```

3. Check NodePool conditions:

```bash
oc get nodepool my-cluster-workers -n clusters -o jsonpath='{.status.conditions}'
```


### IDMS Not Created

If IDMS is not automatically created:

1. Check HostedCluster configuration:

```bash
oc get hostedcluster my-cluster -n clusters -o yaml | grep -A10 imageContentSources
```

2. Check HostedControlPlane:

```bash
oc get hostedcontrolplane my-cluster -n clusters-my-cluster -o yaml | grep -A10 imageContentSources
```

3. Look for reconciliation errors:

```bash
oc logs -n hypershift deployment/operator -c manager | grep -i image
```


### Image Pulls Failing

If images fail to pull even with mirrors configured:

1. Verify mirror registry is accessible from worker nodes
2. Check mirror registry credentials if required
3. Ensure mirrorSourcePolicy is appropriate for your environment
4. Check that the source matches the image repository exactly

## Example: Complete Disconnected Setup

For a fully disconnected environment:

```yaml
---
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: disconnected-cluster
  namespace: clusters
spec:
  imageContentSources:
  - source: registry.redhat.io
    mirrors:
    - internal-registry.corp.com/redhat
  - source: quay.io
    mirrors:
    - internal-registry.corp.com/quay
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: disconnected-tag-mirrors
  namespace: clusters
data:
  config: |
    apiVersion: config.openshift.io/v1
    kind: ImageTagMirrorSet
    metadata:
      name: disconnected-mirrors
    spec:
      imageTagMirrors:
      - source: quay.io/openshift-release-dev
        mirrors:
        - internal-registry.corp.com/openshift-release-dev
        mirrorSourcePolicy: NeverContactSource
---
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: disconnected-cluster-workers
  namespace: clusters
spec:
  clusterName: disconnected-cluster
  replicas: 3
  config:
  - name: disconnected-tag-mirrors
  # ...rest of configuration
```

## Best Practices

1. **Use unified configuration**: Use `imageMirrorConfigRef` as the primary pattern for fleet management to handle both IDMS and ITMS.
2. **Legacy IDMS configuration**: Use `imageContentSources` only for legacy or backwards-compatible scenarios (creates IDMS).
3. **Add ITMS when needed**: Only add ITMS ConfigMaps if you specifically need tag-based mirrors.
4. **Test mirrors first**: Verify mirror registries are accessible before configuring.
5. **Use NeverContactSource in disconnected**: Ensures no attempts to reach external registries.
6. **Monitor mirror health**: Set up monitoring for your mirror registries.
7. **Keep mirrors in sync**: Regularly sync content from upstream to mirrors.

## See Also

- [Image Registry Mirrors Reference](../reference/image-registry-mirrors.md)
- [Disconnected Installation](disconnected/)
- [NodePool Configuration](../reference/api.md#nodepool)
