# HostedCluster Reconciliation Segregation — Implementation Plan

Companion to: [hostedcluster-reconcile-segregation-analysis.md](./hostedcluster-reconcile-segregation-analysis.md)

## Status: Implemented

The reconcile loop has been restructured with categorized error handling. See the analysis
doc for the full design.

## Files Changed

| File | Action |
|------|--------|
| `hypershift-operator/controllers/hostedcluster/reconcile_report.go` | **Created** — report types and methods |
| `hypershift-operator/controllers/hostedcluster/reconcile_report_test.go` | **Created** — unit tests for report |
| `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` | **Modified** — replaced sequential error chain with report-based flow |
| `hypershift-operator/controllers/hostedcluster/hostedcluster_controller_test.go` | **Modified** — added non-blocking behavior tests |

## Operation Classification

| Operation | Category | Rationale |
|-----------|----------|-----------|
| PlatformCredentials | critical | Platform infra operations fail without credentials |
| PullSecretSync | critical | Image pulls fail, pods crash-loop |
| SecretEncryptionSync | critical | Etcd data encryption fails |
| CoreHCPChain | critical | Produces HCP object needed by Phase 8 |
| RestoredFromBackup | nonCritical | Status-only (TODO: move to Phase 4) |
| AuditWebhookSync | nonCritical | Observability, not functional |
| SSHKeySync | nonCritical | Debug access, not operational |
| AdditionalTrustBundle | nonCritical | Custom CAs, not blocking |
| ServiceAccountSigningKey | nonCritical | Conditional, specific feature |
| UnmanagedEtcdMTLS | nonCritical | Only for unmanaged etcd |
| ETCDMemberRecovery | nonCritical | Recovery mechanism, retries naturally |
| GlobalConfigSync | nonCritical | Config propagation, eventual consistency |
| KubeconfigAndPasswordSync | nonCritical | Consumer convenience |
| OperatorDeployments | nonCritical | CPO/CAPI/Karpenter deployments |
| RBACAndPolicies | nonCritical | RBAC/network policies |
| PlatformOIDCAndCSI | nonCritical | Platform-specific (OIDC, CSI, SecretProviderClass) |
| MonitoringAndCLISecrets | nonCritical | Monitoring, CLI secrets, SRE config, trusted CAs |

## Design Principles

1. **Categorize, don't short-circuit** — Operations are classified as critical or non-critical.
   Critical failures block downstream work; non-critical failures are collected and reported.
2. **Minimal structural change** — Extracted blocks into private methods on
   `HostedClusterReconciler`. Individual operation semantics are preserved.
3. **Preserve legitimate gates** — Deletion handling, pause, and config validation gates still
   halt the entire loop. These are intentional.
4. **Structured error reporting** — When critical failures exist, only critical errors are
   surfaced with a list of blocked operations. Non-critical noise is suppressed.
5. **Preserve requeueAfter** — If any operation sets a requeueAfter, it is preserved.
