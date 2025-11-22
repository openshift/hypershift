# Customize Worker Node HAProxy Image

This guide explains how to customize the HAProxy image used for worker node API server proxy on a per-NodePool basis.

## Overview

Worker nodes in HyperShift use HAProxy to proxy connections to the hosted control plane API server. By default, the HAProxy image comes from the OpenShift release payload. However, you can override this image using either:

1. **NodePool annotation** (recommended for per-NodePool customization)
2. **Environment variable** (for global override when shared ingress is enabled)

## Image Resolution Priority

The HAProxy image is resolved in the following priority order (highest to lowest):

1. **NodePool annotation** `hypershift.openshift.io/haproxy-image` (highest priority)
2. **Environment variable** `IMAGE_SHARED_INGRESS_HAPROXY` (when shared ingress is enabled)
3. **Hardcoded default** (when shared ingress is enabled)
4. **Release payload** (default when shared ingress is disabled)

## Per-NodePool Customization

### Use Case

Use the NodePool annotation when you want to:
- Test a newer HAProxy version on a specific NodePool
- Use different HAProxy images for different workload types
- Gradually roll out HAProxy updates across NodePools
- Use a custom HAProxy image with specific patches or configurations

### Configuration

To override the HAProxy image for a specific NodePool, add the `hypershift.openshift.io/haproxy-image` annotation:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: my-nodepool
  namespace: clusters
  annotations:
    hypershift.openshift.io/haproxy-image: "quay.io/my-org/haproxy:custom-v2.9.1"
spec:
  clusterName: my-cluster
  replicas: 3
  # ... rest of spec
```

### Applying the Annotation

You can add the annotation to an existing NodePool using `kubectl annotate`:

```bash
kubectl annotate nodepool my-nodepool \
  -n clusters \
  hypershift.openshift.io/haproxy-image="quay.io/my-org/haproxy:custom-v2.9.1"
```

### Removing the Override

To remove the override and revert to the default behavior:

```bash
kubectl annotate nodepool my-nodepool \
  -n clusters \
  hypershift.openshift.io/haproxy-image-
```

## Global Override (Shared Ingress Only)

When shared ingress is enabled, you can set a global HAProxy image override using the `IMAGE_SHARED_INGRESS_HAPROXY` environment variable on the HyperShift operator. This affects all NodePools that don't have the annotation set.

**Note**: The NodePool annotation always takes precedence over the environment variable.

## Verification

After applying the annotation, new worker nodes will use the specified HAProxy image. To verify:

1. Check the NodePool's token secret generation to ensure the new configuration is picked up
2. Verify the ignition configuration contains the correct image
3. On a worker node, check the static pod manifest:

```bash
# On a worker node
cat /etc/kubernetes/manifests/kube-apiserver-proxy.yaml | grep image:
```

## Rollout Behavior

The HAProxy image change triggers a NodePool rollout:
- New ignition configuration is generated with the updated image
- Worker nodes are replaced according to the NodePool's upgrade strategy
- The rollout respects `maxUnavailable` settings

## Important Notes

1. **Image Availability**: Ensure the custom HAProxy image is accessible from worker nodes
2. **Pull Secrets**: The worker nodes must have credentials to pull the custom image
3. **Compatibility**: The custom HAProxy image must be compatible with HyperShift's configuration expectations
4. **Shared Ingress**: When shared ingress is enabled, ensure the custom image supports proxy protocol v2 with TLV (requires HAProxy v2.9+)
5. **Multiple NodePools**: Each NodePool can have a different HAProxy image override


## Troubleshooting

### Image Pull Errors

If worker nodes fail to pull the custom image:
1. Verify the image exists and is accessible
2. Check that the global pull secret includes credentials for the image registry
3. Verify network connectivity from worker nodes to the image registry

### Wrong Image in Use

If the expected image is not being used:
1. Check the annotation is correctly set on the NodePool
2. Verify the NodePool has reconciled (check status conditions)
3. Inspect the ignition configuration in the NodePool's token secret

### Rollout Issues

If the rollout doesn't complete:
1. Check NodePool conditions for errors
2. Verify the custom image is compatible
3. Check worker node logs for HAProxy startup failures
4. Ensure the image supports the required features (e.g., proxy protocol for shared ingress)