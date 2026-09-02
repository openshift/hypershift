# OSImageStream Behaviour

This section documents how OSImageStream behaves in Hosted Control Planes, including deviations from standalone OpenShift, the container runtime matrix, and upgrade scenarios.

## Background

Starting in OCP 5.0, the `osImageStream` feature graduated from TechPreview to Default. The MachineConfig Operator (MCO) uses OS image streams to determine which RHCOS version (RHEL 9 or RHEL 10) to boot worker nodes with.

During MCC (Machine Config Controller) bootstrap, the controller must discover which OS image streams are available in the release payload and populate `osImageStream.Status` accordingly.

## Expected Behavior for OCP Standalone

In standalone OCP, the MCC bootstrap process calls `fetchOSImageStream()`, which performs **network-based container image inspection** to discover available OS streams from the release payload. This function:

1. Reads the release payload image references.
2. Performs HTTP requests to container registries to inspect image manifests.
3. Uses the cluster's proxy configuration (`HTTP_PROXY`, `HTTPS_PROXY`) to reach the registries.
4. Populates `osImageStream.Status` with the discovered streams (e.g., `rhel-9`, `rhel-10`).

This works because in standalone OCP, the MCC runs on the same network as the cluster and can reach the configured proxy.

## Expected Behavior for Hosted Control Planes

In HyperShift (`ExternalTopologyMode`), the standalone approach does not work. The MCC bootstrap runs inside the **ignition-server pod on the management cluster**, but the proxy configuration comes from the **guest cluster** (e.g., a private VPC IP like `10.0.x.x:3128`). The management cluster cannot reach the guest's proxy, causing all image inspection requests to time out.

### The Fix

The MCO fix ([machine-config-operator#6420](https://github.com/openshift/machine-config-operator/pull/6420), [machine-config-operator#6423](https://github.com/openshift/machine-config-operator/pull/6423)) skips `fetchOSImageStream()` when `ControlPlaneTopology == ExternalTopologyMode` and instead builds `osImageStream.Status` directly from fields that are already available locally:

- `ControllerConfig.Spec.BaseOSContainerImage` (resolved by digest from the release payload)
- `ControllerConfig.Spec.DefaultStream`

This produces the same result without additional network access for OSImageStream discovery, since the release payload already contains the resolved image references. The HyperShift control-plane-operator sets these fields when reconciling the `ControllerConfig` into the hosted cluster's control plane namespace.

### Why This Is Safe

The network-based inspection in standalone OCP is effectively a redundant verification step — the release payload already contains all the information needed to determine OS stream availability. By reading from `ControllerConfig.Spec` fields that the control-plane-operator already populates, the HyperShift path arrives at the same `osImageStream.Status` without crossing the management-to-guest network boundary.

## OSImageStream and Container Runtime Behavior Matrix

The following table documents the expected behavior for each `osImageStream` and container runtime combination in HyperShift. These scenarios were validated through E2E testing during the OSStreams graduation ([CNTRLPLANE-4204](https://issues.redhat.com/browse/CNTRLPLANE-4204)).

### Runtime Availability by RHEL Version

| RHEL Version | Available Runtimes | Default Runtime (OCP 5.0+) | Notes |
|---|---|---|---|
| RHEL 9 (RHCOS 9.x) | `crun`, `runc` | `crun` | Both runtimes available. `runc` can be selected via `ContainerRuntimeConfig` |
| RHEL 10 (RHCOS 10.x) | `crun` only | `crun` | `runc` is **not available**. Requesting `runc` via `ContainerRuntimeConfig` sets `ValidMachineConfig=False` |

### NodePool Scenarios

| Scenario | OCP Version | `spec.osImageStream` | Resulting RHCOS | Runtime Handlers | Outcome |
|---|---|---|---|---|---|
| New cluster, no explicit stream | 5.0+ | (unset) | RHCOS 10 | `crun` | `status.osImageStream` reports `rhel-10` |
| New cluster, explicit `rhel-10` | 5.0+ | `rhel-10` | RHCOS 10 | `crun` | Same as default |
| New cluster, explicit `rhel-9` | 5.0+ | `rhel-9` | RHCOS 9 | `crun`, `runc` | Pin to RHEL 9 — both runtimes available |
| Mixed cluster (rhel-9 + rhel-10 NodePools) | 5.0+ | `rhel-9` on one NP, `rhel-10` on another | RHCOS 9 + RHCOS 10 | Per-NP (see above) | Both coexist, each NP runs its matching OS and runtimes |
| Upgrade 4.x to 5.0+ (no explicit stream) | 4.x → 5.0 | (unset) → `rhel-10` | RHCOS 9 → RHCOS 10 | `runc`,`crun` → `crun` | Default stream changes on upgrade; nodes move to RHEL 10 |
| Upgrade 4.x to 5.0+ (pinned `rhel-9`) | 4.x → 5.0 | `rhel-9` | RHCOS 9 | `crun`, `runc` | Pin preserved across upgrade; nodes stay on RHEL 9 |
| `rhel-10` with `runc` ContainerRuntimeConfig | 5.0+ | `rhel-10` | — | — | **Rejected**: `ValidMachineConfig=False` with reason `ValidationFailed` |
| `rhel-10` on OCP < 5.0 | < 5.0 | `rhel-10` | — | — | **Rejected**: API validation fails (RHEL 10 requires OCP ≥ 5.0) |

### Deviations from Standalone OCP

| Behavior | Standalone OCP | Hosted Control Planes | Notes |
|---|---|---|---|
| OSImageStream discovery | Network-based image inspection (`fetchOSImageStream()`) | Local lookup from `ControllerConfig.Spec` fields | See [The Fix](#the-fix) section above |
| Runtime migration on upgrade (4.x → 5.0) | Existing nodes **do not** auto-migrate from `runc` to `crun` | Existing NodePools **do** migrate on upgrade | Same rationale as the [4.17 to 4.18 migration](upgrade-4.17-4.18.md#runtime-migration-from-runc-to-crun); the decision applies to all major upgrades |
| Runtime migration mechanism (Replace strategy) | N/A | New nodes boot with new payload; old nodes replaced in rolling fashion | New nodes come directly with `crun` on RHEL 10 |
| Runtime migration mechanism (InPlace strategy) | N/A | Nodes cordoned, drained, rebooted with new ignition payload | Reboot applies new MCS templates with `crun` |

## Related References

- **Bug**: [OCPBUGS-112082](https://issues.redhat.com/browse/OCPBUGS-112082) — OSImageStream discovery fails in HyperShift due to proxy unreachable
- **MCO fix**: [machine-config-operator#6420](https://github.com/openshift/machine-config-operator/pull/6420) — Skip `fetchOSImageStream()` for ExternalTopologyMode
- **MCO backport**: [machine-config-operator#6423](https://github.com/openshift/machine-config-operator/pull/6423) — Backport to release branch
- **Feature gate graduation**: [CNTRLPLANE-3871](https://issues.redhat.com/browse/CNTRLPLANE-3871) — OSStreams TechPreview to Default in OCP 5.0
