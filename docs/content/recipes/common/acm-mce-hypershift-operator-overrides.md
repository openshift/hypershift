# Overriding HyperShift Operator Image and Flags in ACM/MCE

## Overview

When HyperShift is deployed via Advanced Cluster Management (ACM) or Multicluster Engine (MCE), the HyperShift addon manages the lifecycle of the HyperShift Operator (HO). In some scenarios, such as testing a hotfix or enabling/disabling specific features, you may need to override the default HO image or modify its install flags.

This guide explains how to use ConfigMaps in the `local-cluster` namespace to customize the HyperShift Operator deployment managed by the ACM/MCE addon.

!!! note

    These overrides only apply when HyperShift is deployed through the ACM/MCE addon (hypershift-addon). They do not apply to standalone HyperShift installations.

## Overriding the HyperShift Operator Image

To deploy a custom HyperShift Operator image instead of the default one bundled with ACM/MCE, create the following ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-override-images
  namespace: local-cluster
data:
  hypershift-operator: <your-custom-image>
```

### Example

```bash
export OVERRIDE_HO_IMAGE="quay.io/myorg/hypershift-operator:latest"

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-override-images
  namespace: local-cluster
data:
  hypershift-operator: ${OVERRIDE_HO_IMAGE}
EOF
```

!!! important

    - The ConfigMap **must** be named `hypershift-override-images` and created in the `local-cluster` namespace.
    - The key `hypershift-operator` maps to the HO image reference that the addon will use for deployment.
    - Once the ConfigMap is created, the hypershift-addon will detect it and redeploy the HyperShift Operator with the specified image.

### Verification

After applying the ConfigMap, verify the operator is running the expected image:

```bash
kubectl get pods -n hypershift -o jsonpath='{.items[*].spec.containers[*].image}' | tr ' ' '\n' | grep hypershift
```

For more details on this mechanism, see the [upstream community documentation](https://github.com/stolostron/hypershift-addon-operator/blob/main/docs/optional/upgrading_hypershift_operator.md).

## Overriding HyperShift Operator Install Flags

To add or remove install flags from the HyperShift Operator deployment managed by the addon, create the following ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: ""
  installFlagsToRemove: ""
```

### Fields

| Field | Description |
|---|---|
| `installFlagsToAdd` | Space-separated list of flags to add to the HO install command. |
| `installFlagsToRemove` | Space-separated list of flags to remove from the HO install command. |

### Example

To enable the defaulting webhook and disable UWM telemetry remote write:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToAdd: --enable-defaulting-webhook true
  installFlagsToRemove: --enable-uwm-telemetry-remote-write
EOF
```

!!! important

    - The ConfigMap **must** be named `hypershift-operator-install-flags` and created in the `local-cluster` namespace.
    - Changes to this ConfigMap trigger a redeployment of the HyperShift Operator with the updated flags.

### Verification

Check the operator deployment args to confirm the flags are applied:

```bash
kubectl get deployment operator -n hypershift -o jsonpath='{.spec.template.spec.containers[0].args}' | jq .
```

For more details, see the [upstream community documentation](https://github.com/stolostron/hypershift-addon-operator/blob/3fbfd69540544b60c65fa4275caa0134de0c0e40/docs/planning/provision_hosted_cluster_on_mce_local_cluster.md).

## Cleanup

To revert to the default behavior, simply delete the ConfigMaps:

```bash
kubectl delete configmap hypershift-override-images -n local-cluster
kubectl delete configmap hypershift-operator-install-flags -n local-cluster
```

The addon will detect the removal and redeploy the HyperShift Operator with the default image and flags.
