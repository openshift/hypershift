---
title: Define Custom KubeAPI Name
---

## What is this for?

`KubeAPIServerDNSName` is a spec field used to declare a custom Kubernetes API URI. To make this work, you simply need to define the URI (e.g., `api.example.com`) in the `HostedCluster` object.

## How does this work?

- This can be defined both during day-1 (initial setup) and day-2 (post-deployment updates).
- The CPO (ControlPlaneOperator) controllers will create a new kubeconfig stored in the HCP namespace. This kubeconfig will be based on certificates and named `custom-admin-kubeconfig`.
- The certificates are generated from the root CA, with their expiration and renewal managed by the `HostedControlPlane`.
- The CPO will report a new kubeconfig, called `CustomKubeconfig`, in the `HostedControlPlane`. This kubeconfig will use the new server defined in the `KubeAPIServerDNSName` field.
- This custom kubeconfig will also be referenced in the `HostedCluster` object under the status field as `CustomKubeconfig`.
- A new secret, named `{HOSTEDCLUSTER_NAME}-custom-admin-kubeconfig`, will be created in the `HostedCluster` namespace. This secret can be used to easily access the HostedCluster API server.

!!! NOTE
    This does not directly affect the dataplane, so no rollouts are expected to occur.

- If you remove this field from the spec, all newly generated secrets and the `CustomKubeconfig` reference will be removed from the cluster and from the status field.

## Additional Notes

This other field called `CustomKubeConfig` is optional and can only be used if `KubeAPIServerDNSName` is not empty. When set, it triggers the generation of a secret with the specified name containing a kubeconfig within the `HostedCluster` namespace. This kubeconfig will also be referenced in the `HostedCluster.status` as `customkubeconfig`. If removed during day-2 operations, all related secrets and status references will also be deleted:

- This action will not cause a NodePool rollout, ensuring zero impact on customers.
- The `HostedControlPlane` object will receive the changes progressed by the Hypershift Operator and delete the corresponding field.
- The `.status.customkubeconfig` will be removed from both `HostedCluster` and `HostedControlPlane` objects.
- The secret in the `HostedControlPlane` namespace, named `custom-admin-kubeconfig`, will be deleted.
- The secret in the `HostedCluster` namespace, named `{HOSTEDCLUSTER_NAME}-custom-admin-kubeconfig`, will also be deleted.




