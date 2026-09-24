---
title: Azure Identity Status Reporting
---

# Azure Identity Status Reporting

## Summary

This document describes the design for `status.platform.azure` on `HostedCluster` and
`HostedControlPlane` resources, which reflects the Azure identity configuration that the
control plane operator (CPO) is actively applying. The primary consumer is the ARO HCP,
which needs this signal to determine when identity replacements have taken effect — enabling
it to safely clean up credentials for old managed identities.

Jira: [CNTRLPLANE-4491](https://redhat.atlassian.net/browse/CNTRLPLANE-4491)

---

## Motivation

### Problem

ARO HCP manages identity replacement: when a control plane managed identity is rotated,
Clusters Service (CS) updates the `credentialsSecretName` fields in the `HostedCluster`
spec with references to the new Key Vault certificates. The CPO then reconciles those
changes into new `SecretProviderClass` objects. The CSI driver picks up the new
certificates and makes them available to control plane pods.

Without a status signal from HyperShift, the ARO HCP cannot observe when the CPO has
applied new identity configuration. This forces consumers to use conservative time-based
delays (e.g. 24 hours) before cleaning up old identity credentials — even though the CPO
typically applies spec changes within one reconcile loop.

### Goals

- Enable consumers to observe which Azure identity configuration the CPO is currently
  applying, without re-deriving it from spec.
- Provide a basis for the ARO HCP to reduce identity cleanup delays after a rotation.
- Cover both `ManagedIdentities` (ARO HCP) and `WorkloadIdentities` (self-managed Azure)
  authentication modes.

### Non-goals

- **Confirming that pods are running with new credentials.** The CPO can confirm it has
  updated the `SecretProviderClass`, but cannot confirm that the CSI driver has rotated the
  mounted volume or that pods have restarted. This is a separate signal tracked in
  [CNTRLPLANE-4495](https://redhat.atlassian.net/browse/CNTRLPLANE-4495).
- **Detecting same-name Key Vault rotations.** If a Key Vault secret is replaced in-place
  (same `credentialsSecretName`, new certificate content), the CPO has no way to detect
  this without Key Vault API calls on every reconcile. Same-name rotation is handled
  transparently by the CSI driver and does not require a reportable `credentialsSecretName`
  change. The ARO identity replacement protocol always uses a new `credentialsSecretName`
  when rotating to a new managed identity, making same-name rotation a non-issue in practice.
- **Verifying that identities work against Azure APIs.** Out of scope.
- **KMS managed identity.** The KMS identity lives in `spec.secretEncryption.kms.azure.kms`
  rather than `spec.platform.azure.azureAuthenticationConfig`. Its status reporting is
  deferred to [CNTRLPLANE-4494](https://redhat.atlassian.net/browse/CNTRLPLANE-4494).

---

## Rotation Contract

The rotation signal visible to HyperShift is a `credentialsSecretName` change in the
`HostedCluster` spec. Consumers must use a new Key Vault secret name when rotating to a new
identity. This is consistent with the ARO identity replacement protocol, where each new
managed identity gets a distinct Key Vault secret name.

When CS passes a new `credentialsSecretName` to HyperShift:

1. CPO reads the updated spec on its next reconcile.
2. CPO updates the corresponding `SecretProviderClass` to reference the new secret name.
3. The CSI driver detects the `SecretProviderClass` change on its next poll (default: 2 min).
4. The CSI driver fetches the new certificate from Key Vault and updates the pod volume mount.
5. The control plane component reads the new certificate and authenticates with the new identity.

This PR provides signal after step 2. Signal after step 4 (pod-level confirmation) is covered
by [CNTRLPLANE-4495](https://redhat.atlassian.net/browse/CNTRLPLANE-4495).

---

## Design

### Status location

`status.platform.azure` is added to both `HostedCluster` and `HostedControlPlane`.

The `HostedControlPlane` status is the authoritative source — the CPO populates it directly.
The HyperShift Operator already copies `hcp.Status.Platform` to `hcluster.Status.Platform`
generically, so the `HostedCluster` status is propagated with no additional code.

### Reporting surface

**Why not ControlPlaneComponent status?**

`ControlPlaneComponent` resources are reconciled generically with no per-component identity
knowledge. They have no way to reflect which `credentialsSecretName` was applied to their
corresponding `SecretProviderClass`.

**Why HCP/HC status?**

The CPO reconciles `HostedControlPlane` and has full visibility into
`spec.platform.azure.azureAuthenticationConfig`. It can mirror the active configuration
into `hcp.Status.Platform.Azure` atomically with every reconcile, using `statuspatching.PatchStatus`.

### What is reported

#### ManagedIdentities mode (ARO HCP)

For each control plane component, the `credentialsSecretName` that the CPO has most recently
applied to the corresponding `SecretProviderClass` is reported. This value changes when an
identity rotation is applied.

For data plane components, the MSI client IDs (passed into the ignition configuration for
worker nodes) are reported.

```
status.platform.azure.managedIdentities.controlPlane:
  cloudProvider.credentialsSecretName:        <string>
  nodePoolManagement.credentialsSecretName:   <string>
  controlPlaneOperator.credentialsSecretName: <string>
  imageRegistry.credentialsSecretName:        <string>
  ingress.credentialsSecretName:              <string>
  network.credentialsSecretName:              <string>
  disk.credentialsSecretName:                 <string>
  file.credentialsSecretName:                 <string>

status.platform.azure.managedIdentities.dataPlane:
  imageRegistryClientID: <string>
  diskClientID:          <string>
  fileClientID:          <string>
```

#### WorkloadIdentities mode (self-managed Azure)

For each component, the workload identity `clientID` that the CPO has applied to the
corresponding `ServiceAccount` annotation is reported.

```
status.platform.azure.workloadIdentities:
  cloudProvider:        <string>
  nodePoolManagement:   <string>
  controlPlaneOperator: <string>
  imageRegistry:        <string>
  ingress:              <string>
  network:              <string>
  disk:                 <string>
  file:                 <string>
```

### Semantic guarantee

!!! important

    `status.platform.azure` reflects configuration the CPO has **applied to
    SecretProviderClass or ServiceAccount objects**. It is not a guarantee that control plane
    pods are currently running with those credentials. The CSI driver poll interval and pod
    restart scheduling introduce a lag between the CPO applying a new `SecretProviderClass`
    and pods mounting the new certificate.

Consumers that require pod-level confirmation should wait for
[CNTRLPLANE-4495](https://redhat.atlassian.net/browse/CNTRLPLANE-4495).

### Reconciliation behavior

The CPO calls `reconcileAzurePlatformStatus` on every reconcile of the `HostedControlPlane`.
The function mirrors the active authentication configuration from spec into status using
`statuspatching.PatchStatus`, which re-fetches the object and applies optimistic locking
to avoid conflicting with concurrent status writers.

Because this is a pure spec-mirror operation (no external API calls), it is safe to run on
every reconcile loop and idempotent on retry.

---

## API Changes

### New types

```go
// AzurePlatformStatus contains status specific to the Azure platform.
//
// +kubebuilder:validation:MinProperties=1
type AzurePlatformStatus struct {
    // managedIdentities reflects the credential references of the managed identities
    // currently applied by the control plane operator. Populated when the Azure
    // authentication mode is ManagedIdentities.
    // +optional
    ManagedIdentities AzureManagedIdentitiesStatus `json:"managedIdentities,omitzero,omitempty"`

    // workloadIdentities reflects the client IDs of the federated workload identities
    // currently applied by the control plane operator. Populated when the Azure
    // authentication mode is WorkloadIdentities.
    // +optional
    WorkloadIdentities AzureWorkloadIdentitiesStatus `json:"workloadIdentities,omitzero,omitempty"`
}

// AzureManagedIdentitiesStatus reflects the active managed identity credential references
// for control plane and data plane components.
//
// +kubebuilder:validation:MinProperties=1
type AzureManagedIdentitiesStatus struct {
    // controlPlane contains the credential secret names currently applied to
    // control plane components by the control plane operator.
    // +optional
    ControlPlane AzureControlPlaneManagedIdentitiesStatus `json:"controlPlane,omitzero,omitempty"`

    // dataPlane contains the MSI client IDs currently applied to data plane components
    // via the ignition configuration.
    // +optional
    DataPlane AzureDataPlaneManagedIdentitiesStatus `json:"dataPlane,omitzero,omitempty"`
}

// AzureControlPlaneManagedIdentitiesStatus reflects the active Key Vault credential
// secret name per control plane component. Each field holds the credentialsSecretName
// that the CPO is currently using; it changes when an identity rotation is applied.
//
// +kubebuilder:validation:MinProperties=1
type AzureControlPlaneManagedIdentitiesStatus struct {
    CloudProvider        string `json:"cloudProvider,omitempty"`
    NodePoolManagement   string `json:"nodePoolManagement,omitempty"`
    ControlPlaneOperator string `json:"controlPlaneOperator,omitempty"`
    ImageRegistry        string `json:"imageRegistry,omitempty"`
    Ingress              string `json:"ingress,omitempty"`
    Network              string `json:"network,omitempty"`
    Disk                 string `json:"disk,omitempty"`
    File                 string `json:"file,omitempty"`
}
```

### PlatformStatus change

`Azure AzurePlatformStatus` is added to the existing `PlatformStatus` struct alongside
the existing `AWS *AWSPlatformStatus` field.

---

## Alternatives Considered

### Report clientID instead of credentialsSecretName

**Rejected (for control plane managed identities).**

`ManagedIdentity.clientID` is optional in the spec and documented as "mainly used for CI
purposes." ARO HCP production clusters do not set this field today (confirmed by the ARO
team). Reporting it would result in empty status fields for all production clusters.

`credentialsSecretName` is always required and is the operative identifier that changes
when a rotation is applied. It is already present in the public spec, so reflecting it in
status does not expose new internal state.

!!! note "Future consideration"

    If ARO starts setting `clientID` on control plane managed identities — for example, to
    enable deterministic `SecretProviderClass` naming based on identity — the API can be
    extended to report it alongside `credentialsSecretName`. The spec field already exists.

### Same-name Key Vault rotation detection

**Out of scope.**

Detecting same-name rotation would require Key Vault API calls on every reconcile loop,
which does not scale. Same-name rotation is handled transparently by the CSI driver.
The ARO identity replacement protocol uses a distinct `credentialsSecretName` per identity,
so this case does not arise in practice.

### ControlPlaneComponent status

**Rejected.** `ControlPlaneComponent` resources are reconciled generically without
per-component identity knowledge. HCP/HC status is the correct surface.

---

## Future Work

| Ticket | Description |
|--------|-------------|
| [CNTRLPLANE-4494](https://redhat.atlassian.net/browse/CNTRLPLANE-4494) | Extend status to include the KMS managed identity (`spec.secretEncryption.kms.azure.kms`). |
| [CNTRLPLANE-4495](https://redhat.atlassian.net/browse/CNTRLPLANE-4495) | Pod-level confirmation via `SecretProviderClassPodStatus`. Watch per-pod CSI driver status objects to report when all pods for a given component have mounted the new credential. This enables consumers to eliminate time-based cleanup delays entirely. |
| [CNTRLPLANE-4496](https://redhat.atlassian.net/browse/CNTRLPLANE-4496) | E2e test coverage for both `ManagedIdentities` and `WorkloadIdentities` authentication modes. |

---

## Open Questions (Resolved)

**Q: Should we report `clientID` alongside `credentialsSecretName` for control plane identities?**

Resolved: No, for now. `clientID` is optional in the spec and not set in ARO HCP production
clusters. `credentialsSecretName` is the operative rotation signal. If ARO begins setting
`clientID`, the API can be extended.

**Q: Can HyperShift detect same-name Key Vault rotations?**

Resolved: No. Detecting same-name rotation requires Key Vault API calls per reconcile, which
does not scale. The ARO protocol always changes `credentialsSecretName` on rotation, making
this case moot.

**Q: What granularity does Clusters Service need — per-identity-role or a simpler rollup?**

Resolved: Per-identity-role. CS and the ARO HCP track individual managed identities by
component and need to know which specific `credentialsSecretName` is active per component.
A rollup (e.g., single boolean "all rotated") would not provide enough information.

**Q: Do all control plane components have msi-dataplane auto-refresh enabled?**

From [Mulham's feasibility analysis on OCPSTRAT-2151](https://redhat.atlassian.net/browse/OCPSTRAT-2151?focusedCommentId=18114313):
the msi-dataplane library supports auto-refresh of credentials without a pod restart.
This needs to be verified for each control plane component to confirm that identity
rotation does not require a rolling restart of control plane pods.

*Status: open — verification pending.*

**Q: Is CPC status a viable reporting surface?**

Resolved: No. `ControlPlaneComponent` status is reconciled generically with no per-component
identity knowledge. HCP/HC status is the correct surface.
