# HostedCluster Controller Reconciliation Loop — Segregation Analysis

## Problem Statement

The `reconcile()` function in `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go`
executes ~50 distinct operations in a strictly sequential chain where **every error
causes an early return**, short-circuiting the entire remaining loop. This means an unrelated failure
(e.g., missing SSH key secret) prevents critical operations like deploying the control plane operator
or reconciling the HostedControlPlane object.

## Implemented Solution

### Error Categorization

Operations are classified into two categories using a `reconcileReport` struct
(`reconcile_report.go`):

- **critical**: Failures block all downstream Phase 8 component operations.
  Examples: PullSecretSync, PlatformCredentials, SecretEncryptionSync, CoreHCPChain.
- **nonCritical**: Failures are collected but never block other work.
  Examples: SSHKeySync, AuditWebhookSync, MonitoringAndCLISecrets.

### Phase Structure

```
Phase 0-4:  Initialization, status conditions, deletion handling     — unchanged
Phase 5:    Prerequisites — finalizers, pause, validation gates      — unchanged (fail-fast)

Phase 6a:   Critical sync (error-collecting within phase)
            - PlatformCredentials
            - PullSecretSync
            - SecretEncryptionSync

Phase 6b:   Non-critical sync (error-collecting, never blocked)
            - RestoredFromBackup (TODO: move to Phase 4)
            - AuditWebhookSync
            - SSHKeySync
            - AdditionalTrustBundle
            - ServiceAccountSigningKey
            - UnmanagedEtcdMTLS
            - ETCDMemberRecovery
            - GlobalConfigSync

Phase 7:    Core HCP chain — sequential (HCP → InfraCR → CAPI Cluster)
            Always runs regardless of Phase 6a failures.

Phase 8:    Component groups — blocked if any critical operation failed.
            Uses executeOrBlock/executeOrBlockMulti to automatically skip
            when hasCriticalFailure() is true:
            - KubeconfigAndPasswordSync
            - OperatorDeployments (CPO, CAPI Manager, CAPI Provider, Karpenter)
            - RBACAndPolicies (Prometheus RBAC, PKI RBAC, Network Policies)
            - PlatformOIDCAndCSI (KubeVirt CSI / AWS OIDC / Azure SecretProviderClass)
            - MonitoringAndCLISecrets (CLI secrets, dashboard, SRE metrics, trusted CAs)
```

### Blocking Rules

- **Phase 6a critical failure** → Phase 8 blocked (Phase 7 still runs)
- **Phase 7 failure / nil HCP** → Phase 8 blocked
- **Phase 6b non-critical failure** → never blocks anything

### Error Aggregation

When critical failures exist, `aggregate()` returns only critical errors plus a summary of
blocked operations — non-critical errors are suppressed since the user should fix the
critical issue first:

```
critical error: failed to get pull secret...; blocked operations: [KubeconfigAndPasswordSync, OperatorDeployments, RBACAndPolicies, PlatformOIDCAndCSI, MonitoringAndCLISecrets]
```

When no critical failures exist, all errors are returned as-is via `utilerrors.NewAggregate`.

### Key Types

```go
// reconcile_report.go

type operationCategory int
const (
    critical    operationCategory = iota
    nonCritical
)

type reconcileReport struct {
    results      []operationResult
    requeueAfter *time.Duration
}
```

Key methods:
- `recordOp(name, category, err)` — record a single operation result
- `recordBlocked(name, category)` — record a skipped operation
- `recordErrors(name, category, []error)` — record a group operation
- `executeOrBlock(name, category, fn)` — run fn or record blocked if critical failure exists
- `executeOrBlockMulti(name, category, fn)` — same for multi-error functions
- `hasCriticalFailure()` — any critical op failed?
- `aggregate()` — final error for reconcile return value
- `conditionMessage()` — structured summary for logging

## Impact Summary

**Before**: An error in any of ~50 operations blocks all subsequent operations. Examples:
- SSH key secret missing → CPO never deployed → control plane never starts
- Monitoring dashboard error → karpenter operator never reconciled
- AWS resource tags error → HCP never updated

**After**: Only genuinely dependent operations block each other. Examples:
- SSH key secret missing → SSH key not synced (reported), but HCP object still created
  (Phase 7), CPO still deployed (Phase 8), CAPI still works
- Pull secret sync failure (critical) → Phase 8 components blocked with clear reporting
  of what failed and what was skipped, but Phase 7 still runs
- Platform credential error (critical) → same blocking behavior, user sees exactly which
  operations were skipped
- Audit webhook failure (non-critical) → reported, but everything else proceeds normally
