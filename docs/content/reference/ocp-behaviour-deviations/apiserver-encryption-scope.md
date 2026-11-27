# API Server Encryption Scope

This section documents how `spec.configuration.apiServer.encryption` behaves in Hosted
Control Planes and where it deviates from standalone OpenShift.

## Background

OpenShift supports encryption at rest for etcd-backed resources, configured through the
cluster-scoped `APIServer` config (`apiServer.encryption.type`). Prior to
[OCPBUGS-56376](https://issues.redhat.com/browse/OCPBUGS-56376), setting
`spec.configuration.apiServer.encryption` on a HostedCluster was a silent no-op: only the
HyperShift-specific `spec.secretEncryption` field was wired into the kube-apiserver
deployment. HyperShift now honors `apiServer.encryption.type: aescbc` by auto-provisioning
an AESCBC key and populating `spec.secretEncryption` when the user has not configured it
directly. `spec.secretEncryption` continues to take precedence when both are set.

## Expected Behavior for OCP Standalone

In standalone OpenShift, enabling `apiServer.encryption` encrypts a broader set of
resources — Secrets, ConfigMaps, and (depending on the release) Routes and the OAuth
token kinds — and the cluster-kube-apiserver-operator performs a staged key rollout and a
storage migration that re-encrypts existing objects before reporting completion.

## Expected Behavior for Hosted Control Planes

In HyperShift, `apiServer.encryption` can only select `aescbc` (see the rows below), and
the AESCBC `EncryptionConfiguration` covers **Secrets only** (see `support/config/kms.go`,
`AESCBCEncryptedObjects()`). ConfigMaps, Routes, and OAuth token objects are **not**
encrypted at rest under AESCBC, regardless of what the upstream `apiServer.encryption.type`
field description implies. This matches the pre-existing AESCBC `spec.secretEncryption`
path; honoring `apiServer.encryption` does not widen it. (KMS encryption, which is
configured only through `spec.secretEncryption` and is not selectable via this field,
declares a broader set in `KMSEncryptedObjects()`.)

### Deviations from Standalone OCP

| Behavior | Standalone OCP | Hosted Control Planes | Notes |
|---|---|---|---|
| Encrypted resource scope (AESCBC) | Secrets, ConfigMaps, Routes, OAuth tokens | **Secrets only** | `support/config/kms.go` `AESCBCEncryptedObjects()` |
| `aesgcm` encryption type | Supported (feature-gated) | **Rejected on create / switch** | Creating or switching to `aesgcm` is rejected; a HostedCluster that already stores `aesgcm` can still be updated (incl. unrelated and `/status` updates). Use `aescbc`, or `spec.secretEncryption` with type `kms` |
| KMS via `apiServer.encryption.type` | Supported (feature-gated) | **Not selectable** | KMS is configured exclusively through `spec.secretEncryption` |
| Disabling encryption (`aescbc` → `identity`) | Supported via staged migration | **Rejected at admission** | Disabling encryption after it has been enabled is not supported |

## Related References

- **Bug**: [OCPBUGS-56376](https://issues.redhat.com/browse/OCPBUGS-56376) — `apiServer.encryption` is a silent no-op in HostedCluster
