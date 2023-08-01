# Automatically Initialize RegistryOverrides with Image Content Type Policies
## General
The HyperShift Operator (HO) will automatically initialize the control plane operator (CPO) with any image registry override information from any ImageContentSourcePolicy (ICSP) or any ImageDigestMirrorSet (IDMS) instances from an OpenShift management cluster.

!!! note

    OpenShift management clusters do not allow both ICSP and IDMS CR instances.
    IDMS CRs should be used with OpenShift release image versions 4.13 or higher.
    ICSPs will be deprecated in future OpenShift release versions.

## Technical Implementation Details
### General
In `hypershift-operator/main.go`, the HO will look to see if its management cluster has either ICSP or IDMS capabilities. If so, it will retrieve the image registry information from the appropriate ICSP or IDMS instances and store them in a variable called `imageRegistryOverrides`. This variable is then provided to a custom release image provider called `ProviderWithOpenShiftImageRegistryOverrides`.

### ProviderWithOpenShiftImageRegistryOverrides
The `Lookup` function for `ProviderWithOpenShiftImageRegistryOverrides` will use the source and mirror information in `imageRegistryOverrides` when attempting to look up release images.
The `ProviderWithOpenShiftImageRegistryOverrides` is also provided to the `HostedClusterReconciler` as its `ReleaseProvider`. When the `HostedClusterReconciler` is reconciling, it will pass any image registry overrides to the ignition server reconciler and the CPO deployment specification.

### Ignition Server
The ignition server reconciler forwards this information on to the `ignition-server` container as an environment variable called `OPENSHIFT_IMG_OVERRIDES`. `hypershift/ignition-server/cmd/start.go` retrieves this information for the `ReleaseProvider` for the `TokenSecretReconciler`. An important caveat for the ignition server, it cannot follow the ImageContentSourcePolicy or ImageDigestMirrorSet rules because there is not a runtime running inside the pod to do the transformations. So, it's necessary to do the image URL live translation to the custom registry address.

### Control Plane Operator
The `HostedClusterReconciler` passes on the image registry override information as an environment variable in the CPO called `OPENSHIFT_IMG_OVERRIDES`. The CPO will check for the existence of this environment variable when it runs. If the variable exists, it's used to build the HostedControlPlaneReconciler's `releaseProvider`.