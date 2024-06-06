## Configuring disconnected HostedControlPlanes deployments

!!! Note

    This section is mainly focused on the Management Cluster side. If you want to see how to configure the HostedCluster's ImageContentSource, please check out [this other documentation section](./image-content-sources.md).

HostedControlPlanes operate in two main domains: the Control Plane, which is part of the Management Cluster, and the Data Plane, which resides in the worker nodes deployed by the customer.

The ICSP/IDMS for the Data Plane is managed via the `ImageContentSources` API in the HostedCluster manifests and this is the only source of truth for the DataPlane. It is also possible to modify the registry files directly in the workers using the [MachineConfig](../automated-machine-management/configure-machines.md) method, changing configurations not directly supported via the API of those ICSP/IDMS objects.

For the Control Plane, ICSP/IDMS objects are managed in the Management Cluster. These objects are parsed by the Hypershift Operator and shared as `registry-overrides` with the ControlPlaneOperator. These entries will be injected into any of the deployments in the HostedControlPlane namespace as an argument.

In summary, to work with disconnected registries in the HostedControlPlane, you first need to create the appropriate ICSP/IDMS in the Management Cluster. Then, to deploy disconnected workloads in the Data Plane, you need to add the desired entries into the ImageContentSources field inside the HostedCluster manifest.

### Debugging IDMS/ICSP Propagation

You can check if the entries created in the ICSP/IDMS are being propagated properly checking the deployments in the HostedControlPlane. For instance, if you check the ignition-server deployment you can see the command the pod will execute, you need to check if the `--registry-overrides` flag is followed by a serie of tuples separated by comma:

```
...
 - --registry-overrides
 - quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:922dd705c2e05c88dc328b7ad4651d5af053851ea6a47dc52840d1abdde3926e=registry.sample.net/quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:922dd705c2e05c88dc328b7ad4651d5af053851ea6a47dc52840d1abdde3926e,quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:f4196370cd6348094a11d9384bae3c88a05af2fdd0ed32e919bea26943a939c2=registry.sample.net/quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:f4196370cd6348094a11d9384bae3c88a05af2fdd0ed32e919bea26943a939c2
...
```

If that so, means the ICSP/IDMS are being propagated properly, otherwise there is some kind of problem. Please if that's the case, check the [known-issues section](known-issues.md) for more info.


## Technical Implementation Details

### General

In `hypershift-operator/main.go`, the HO will look to see if its management cluster has either ICSP or IDMS capabilities. If so, it will retrieve the image registry information from the appropriate ICSP or IDMS instances and store them in a variable called `imageRegistryOverrides`. This variable is then provided to a custom release image provider called `ProviderWithOpenShiftImageRegistryOverrides`.

### Provider With OpenShift Image Registry Overrides

The `Lookup` function for `ProviderWithOpenShiftImageRegistryOverrides` will use the source and mirror information in `imageRegistryOverrides` when attempting to look up release images.
The `ProviderWithOpenShiftImageRegistryOverrides` is also provided to the `HostedClusterReconciler` as its `ReleaseProvider`. When the `HostedClusterReconciler` is reconciling, it will pass any image registry overrides to the ignition server reconciler and the CPO deployment specification.

### Ignition Server

The ignition server reconciler forwards this information on to the `ignition-server` container as an environment variable called `OPENSHIFT_IMG_OVERRIDES`. `hypershift/ignition-server/cmd/start.go` retrieves this information for the `ReleaseProvider` for the `TokenSecretReconciler`. An important caveat for the ignition server, it cannot follow the ImageContentSourcePolicy or ImageDigestMirrorSet rules because there is not a runtime running inside the pod to do the transformations. So, it's necessary to do the image URL live translation to the custom registry address.

### Control Plane Operator
The `HostedClusterReconciler` passes on the image registry override information as an environment variable in the CPO called `OPENSHIFT_IMG_OVERRIDES`. The CPO will check for the existence of this environment variable when it runs. If the variable exists, it's used to build the HostedControlPlaneReconciler's `releaseProvider`.

#### OLM Catalogs

The imageStream used for the OLM catalogs, when using the `management` (default) OLMCatalogPlacement mode, is not automatically amended with override information detected from ImageContentSourcePolicy (ICSP) on the management cluster.
This because there is not an easy way to validate them in advance for imagestreams.
In case the OLM catalogs got properly mirrored to an internal registry (using the original name and tag), the guest cluster owner can use the `hypershift.openshift.io/olm-catalogs-is-registry-overrides` annotation on the HostedCluster CR.
The format is: `"sr1=dr1,sr2=dr2"` having the source registry string as a key and the destination registry string as value.
OLM catalog image addresses, before being applied to the imagestream, are scanned for the source registry string and if found the string is replaced with the destination registry one.
The cluster admin will also be able to bypass the whole OLM catalogs imagestream mechanism using 4 annotations (`hypershift.openshift.io/certified-operators-catalog-image`, `hypershift.openshift.io/community-operators-catalog-image`, `hypershift.openshift.io/redhat-marketplace-catalog-image`, `hypershift.openshift.io/redhat-operators-catalog-image`) on the HostedCluster CR to directly specify the address (only by digest) of the 4 images to be used for OLM operator catalogs.
In this case the imageStream is not going to be created, and it will be up to the guest cluster owner updating the value of the annotations when the internal mirror will get refreshed to pull in operator updates.
Please notice that if this override mechanism is required, all the 4 values for the 4 default catalog sources are needed.

## References

- [How to configure ICSP/IDMS for the Management clusters](https://access.redhat.com/documentation/en-us/openshift_container_platform/4.15/html/images/image-configuration)
- [Hypershift ImageContentSource details](./image-content-sources.md)
- [Configuring Machines in HyperShift](../automated-machine-management/configure-machines.md)