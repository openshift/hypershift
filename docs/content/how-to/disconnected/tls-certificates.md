# How to Configure TLS Certificates for Disconnected Deployments

In the HostedControlPlane area, it is necessary to configure the registry CA certificates in several areas to ensure proper functioning of disconnected deployments.

## Adding the Registry CA to the Management Cluster

There are numerous methods to accomplish this within the OpenShift environment. However, we have chosen a less intrusive approach.

1. First, create a ConfigMap with a name of your choosing. In this example, we will use the name `registry-config`. The content should resemble the following:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: registry-config
  namespace: openshift-config
data:
  registry1.hypershiftbm.lab..5000: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
  registry2.hypershiftbm.lab..5000: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
  registry3.hypershiftbm.lab..5000: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
```

!!! note

    The `data` field should contain the registry name, while the value should include the registry certificate. As shown, the ":" character is replaced by ".."; ensure this correction is made.

Ensure your data in the ConfigMap is defined using only `"|"` instead of other methods like `"|-"`. Using other methods can cause issues when the Pod reads the certificates loaded in the system.

2. Now, we need to patch the cluster-wide object `image.config.openshift.io` to include this:

```yaml
spec:
  additionalTrustedCA:
    - name: registry-config
```

This modification will result in two significant outcomes:

- Granting master nodes the capability to retrieve images from the private registry.
- Allowing the Hypershift Operator to extract the OpenShift payload for the HostedCluster deployments.

!!! note

    The modification may take several minutes to be successfully executed.

## Adding the Registry CA to the HostedCluster Worker Nodes

This configuration allows the HostedCluster to inject the CA into the DataPlane workers, which is necessary for pulling images from the private registry.

This is defined in the field `hc.spec.additionalTrustBundle` as follows:

```yaml
spec:
  additionalTrustBundle:
    name: user-ca-bundle
```

This `user-ca-bundle` entry is a ConfigMap created by the user in the HostedCluster namespace (the same namespace where the HostedCluster object is created).

The ConfigMap should look like this:

```yaml
apiVersion: v1
data:
  ca-bundle.crt: |
    // Registry1 CA
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----

    // Registry2 CA
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----

    // Registry3 CA
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----

kind: ConfigMap
metadata:
  name: user-ca-bundle
  namespace: <HOSTEDCLUSTER NAMESPACE>
```

This modification will result in two significant outcomes:

- Granting HostedCluster workers the capability to retrieve images from the private registry.
