# Adding a New Cluster Capability

This guide describes how to add support for a new optional capability to the HostedCluster API, following the pattern established by the existing capabilities (ImageRegistry, Console, NodeTuning, etc.).

For user-facing documentation on how capabilities work, see [Cluster Capabilities](../how-to/cluster-capabilities.md).

## Background: OpenShift Capabilities

HyperShift's capability system builds on top of OpenShift's `ClusterVersionCapability` type defined in the [openshift/api](https://github.com/openshift/api) module. The full set of known capabilities is defined in [`config/v1/types_cluster_version.go`](https://github.com/openshift/api/blob/master/config/v1/types_cluster_version.go) as the `KnownClusterVersionCapabilities` list and includes:

| OpenShift Capability | Supported in HyperShift |
|---|---|
| `openshift-samples` | Yes |
| `baremetal` | Yes |
| `marketplace` | No |
| `Console` | Yes |
| `Insights` | Yes |
| `Storage` | No |
| `CSISnapshot` | No |
| `NodeTuning` | Yes |
| `MachineAPI` | No |
| `Build` | No |
| `DeploymentConfig` | No |
| `ImageRegistry` | Yes |
| `OperatorLifecycleManager` | No |
| `OperatorLifecycleManagerV1` | No |
| `CloudCredential` | No |
| `Ingress` | Yes |
| `CloudControllerManager` | No |

When adding a new capability to HyperShift, the value **must** correspond to an existing `ClusterVersionCapability` constant from that upstream list. HyperShift's `CalculateEnabledCapabilities` function uses the upstream `ClusterVersionCapabilitySetCurrent` as the default set and then applies the explicitly enabled/disabled overrides. See `support/capabilities/hosted_control_plane_capabilities.go:68-90`.

## Implementation Checklist

### 1. Define the Capability Constant

Add a new `OptionalCapability` constant in [`api/hypershift/v1beta1/hostedcluster_types.go`](https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/hostedcluster_types.go) alongside the existing ones (lines 477-483). The value must reference the corresponding `configv1.ClusterVersionCapability`:

```go
const MyCapability OptionalCapability = OptionalCapability(configv1.ClusterVersionCapabilityMyCapability)
```

Update the `// +kubebuilder:validation:Enum=...` tag on the `OptionalCapability` type (line 474) to include the new value.

### 2. Add a Capability Helper Function

Add an `Is<Name>CapabilityEnabled` function in [`support/capabilities/hosted_control_plane_capabilities.go`](https://github.com/openshift/hypershift/blob/main/support/capabilities/hosted_control_plane_capabilities.go). Follow the existing pattern:

```go
func IsMyCapabilityEnabled(capabilities *hyperv1.Capabilities) bool {
    if capabilities == nil {
        return true
    }
    for _, disabledCap := range capabilities.Disabled {
        if disabledCap == hyperv1.MyCapability {
            return false
        }
    }
    return true
}
```

### 3. Wire It Into the Controllers

Depending on where the component's resources are reconciled, add checks to skip reconciliation when the capability is disabled. Common integration points:

- **Control Plane Operator (CPO)** controllers under `control-plane-operator/controllers/hostedcontrolplane/` for control-plane-side operator deployments.
- **Hosted Cluster Config Operator (HCCO)** controllers under `control-plane-operator/hostedclusterconfigoperator/controllers/resources/` for guest-cluster-side resources.
- **CPOv2 components** under `control-plane-operator/controllers/hostedcontrolplane/v2/` for components using the new control plane component framework. Use the `IsDisabled()` method on the component to gate reconciliation based on capability state.

Look at how existing capabilities gate their resources. For example:

- `IsImageRegistryCapabilityEnabled` is checked in the registry operator component, nodepool controller, and KAS params.
- `IsNodeTuningCapabilityEnabled` is checked in the NTO component and config operator.
- `IsIngressCapabilityEnabled` is checked in the ingress operator component and config operator.

### 4. Update the CLI Help Text

Update the `--disable-cluster-capabilities` flag description in [`cmd/cluster/core/create.go`](https://github.com/openshift/hypershift/blob/main/cmd/cluster/core/create.go) (line 111) to include the new value in the supported values list.

### 5. Regenerate CRDs and Vendored API

Since `api/` is a separate Go module, you must run:

```shell
make update
```

This regenerates CRDs, revendors the API module, and updates generated clients.

### 6. Add Tests

Add test cases for the new helper function in [`support/capabilities/hosted_control_plane_capabilities_test.go`](https://github.com/openshift/hypershift/blob/main/support/capabilities/hosted_control_plane_capabilities_test.go). Cover:

- Capability enabled by default (nil `Capabilities`).
- Capability explicitly disabled.
- Capability not in the disabled list (still enabled).

### 7. Add CEL Validation (If Needed)

If the new capability has cross-capability dependencies (like Ingress requiring Console to also be disabled), add a CEL validation rule on the `Disabled` field in the `Capabilities` struct (line 514 area). See the existing Ingress/Console constraint as an example:

```go
// +kubebuilder:validation:XValidation:rule="!self.exists(cap, cap == 'Ingress') || self.exists(cap, cap == 'Console')",message="Ingress capability can only be disabled if Console capability is also disabled"
```

## Reference PRs

These PRs show the full implementation pattern for each capability that has been added:

| Capability | PR | Description |
|---|---|---|
| ImageRegistry | [#5456](https://github.com/openshift/hypershift/pull/5456), [#5964](https://github.com/openshift/hypershift/pull/5964) | Initial implementation behind feature gate, then GA and backport to main |
| openshift-samples | [#6197](https://github.com/openshift/hypershift/pull/6197) | Disabling the Samples operator |
| Insights | [#6246](https://github.com/openshift/hypershift/pull/6246) | Disabling the Insights operator |
| Console | [#6183](https://github.com/openshift/hypershift/pull/6183) | Disabling the Console operator (generic disable framework) |
| NodeTuning | [#6356](https://github.com/openshift/hypershift/pull/6356) | Disabling the Node Tuning Operator |
| Ingress | [#6319](https://github.com/openshift/hypershift/pull/6319) | Disabling the Ingress operator (with Console dependency constraint) |
| baremetal | [#6158](https://github.com/openshift/hypershift/pull/6158) | Enable field support and baremetal default exclusion |

The best PRs to use as a template are the simpler single-capability additions like [#6197](https://github.com/openshift/hypershift/pull/6197) (openshift-samples) or [#6246](https://github.com/openshift/hypershift/pull/6246) (Insights).

## Key Code Locations

| What | Path |
|---|---|
| `OptionalCapability` type and constants | `api/hypershift/v1beta1/hostedcluster_types.go:474-483` |
| `Capabilities` struct and CEL rules | `api/hypershift/v1beta1/hostedcluster_types.go:485-516` |
| `HostedClusterSpec.Capabilities` field | `api/hypershift/v1beta1/hostedcluster_types.go:829-835` |
| `HostedControlPlaneSpec.Capabilities` field | `api/hypershift/v1beta1/hosted_controlplane.go:265-266` |
| Capability helper functions | `support/capabilities/hosted_control_plane_capabilities.go` |
| Capability helper tests | `support/capabilities/hosted_control_plane_capabilities_test.go` |
| `CalculateEnabledCapabilities` (net capability set) | `support/capabilities/hosted_control_plane_capabilities.go:68-90` |
| CLI flag definition | `cmd/cluster/core/create.go:111` |
| CLI capability wiring | `cmd/cluster/core/create.go:361-368, 767-779` |
| OpenShift `ClusterVersionCapability` definitions | [`openshift/api` `config/v1/types_cluster_version.go`](https://github.com/openshift/api/blob/master/config/v1/types_cluster_version.go) |
