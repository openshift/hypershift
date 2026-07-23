---
title: July 2026 Progress Report
description: Etcd sharding, agentic CI pipelines, KMS key rotation convergence, Azure GA sprint, dual-stream RHEL plumbing, production bug fixes, reconciler refactor, and the rise of bot-authored PRs — 254 PRs from 57 contributors.
---

# July 2026 Progress Report

<div class="grid cards" markdown>

-   :octicons-git-pull-request-24:{ .lg } **254** PRs merged
-   :octicons-people-24:{ .lg } **57** contributors
-   :octicons-bug-24:{ .lg } **18** customer-reported fixes
-   :octicons-clock-24:{ .lg } **300.6h** avg merge time

</div>

HyperShift's June 23 to July 22 window tells two stories at once. The first is about scaling ceilings — etcd sharding to mitigate event storms on high-node, high-activity clusters, a KMS key rotation race condition exposed by back-to-back rotations, and a batch of subtle production bugs whose intermittent symptoms made them difficult to isolate. The second is about a shift in how the code gets written: bot-authored PRs now account for a significant fraction of merged work, and the infrastructure to make that sustainable — authorization gates, eval harnesses, telemetry pipelines — matured substantially.

Fifty-seven contributors participated across the hypershift, release, enhancements, and ai-helpers repositories. Thirty-four PRs came from bots (cherry-picks, dependency updates, Jira-driven fixes). The average time from PR open to merge was 300.6 hours — about 12.5 days — reflecting both the project's review culture and the complexity of changes that increasingly span multiple repositories and platforms. Eighteen PRs addressed customer-reported bugs, several of which had been silently impacting production clusters.

What follows are eight stories from this period, chosen because they best illustrate the technical challenges, the engineering judgment, and the new patterns defining this project.

---

## :material-database-cog: Etcd Sharding by Resource Kind

On high-node, high-activity clusters, etcd becomes the bottleneck. Not because of data volume — the problem is contention. Events and Leases generate write storms that drown out the reads other controllers need to function. The single-etcd-cluster architecture works fine at enterprise scale, but when pushing beyond tested limits the write contention becomes a ceiling that no amount of hardware can fix.

[@jhjaggars](https://github.com/jhjaggars) attacked this problem at the design level first. Enhancement [#1979](https://github.com/openshift/enhancements/pull/1979) lays out a sharding strategy that distributes high-churn resource kinds across independent etcd deployments. Each shard is registered as its own `ControlPlaneComponent` via the CPOv2 framework, inheriting priority classes, topology spread constraints, scale-to-zero, and pod disruption budgets for free. KAS routes requests to the correct shard through `--etcd-servers-overrides`, a mechanism that already exists in upstream Kubernetes but was never wired into HyperShift.

The implementation landed in [PR #8705](https://github.com/openshift/hypershift/pull/8705), a 50-file PR touching API types, CRD manifests, CPO controllers, and CLI. New API types — `EtcdShardResource`, `ManagedEtcdShardSpec`, `ManagedEtcdShardStorageSpec`, `EtcdShardSchedulingSpec`, `UnmanagedEtcdShardSpec` — give cluster administrators fine-grained control over per-shard storage (PVC or EmptyDir), scheduling (nodeSelector, tolerations), and TLS. Each shard gets its own StatefulSet, TLS certificates, ServiceMonitor, and PDB, all managed independently.

The design makes a deliberate trade-off: shard lists are immutable after creation. No live resharding. This avoids the distributed systems nightmare of data migration between etcd instances while a cluster is running. It also means the initial shard configuration is a permanent architectural decision, which the CEL validation rules and comprehensive documentation make explicit. The feature is gated behind `EtcdSharding` in `TechPreviewNoUpgrade`.

!!! tip "Key Takeaway"
    This is the infrastructure that unlocks the next tier of scale for managed services. By isolating high-churn resources into dedicated etcd instances, the per-resource write contention that caps single-etcd deployments is eliminated without requiring changes to upstream Kubernetes or CAPI.

---

## :material-robot-industrial: The Agentic CI Pipeline

A year ago, the idea of AI agents opening pull requests against production infrastructure code would have been science fiction. Today, `hypershift-jira-solve-ci` is one of the most prolific contributors to the HyperShift repository, and this month the team built out the infrastructure to make that sustainable.

The evolution started with architecture. [@enxebre](https://github.com/enxebre)'s [release #81217](https://github.com/openshift/release/pull/81217) replaced a 1,399-line monolithic review-agent process script with a 210-line wrapper around the `openshift-developer` plugin's `/address-review-pr` skill — a net reduction of 1,647 lines. But the presubmit model had a fundamental flaw: when the agent pushed commits to a PR branch, the presubmit job that spawned it got cancelled. [Release #81366](https://github.com/openshift/release/pull/81366) solved this by splitting the architecture into a lightweight presubmit trigger that fires a periodic job via the Gangway API, decoupling the agent's lifecycle from the PR's CI pipeline.

The jira-agent also grew cross-repo capabilities. [Release #82149](https://github.com/openshift/release/pull/82149) added PAT authentication mode for cross-repo PR creation, with `openshift/installer` as the first onboarded repo. The agent now auto-forks into `jira-solve-bot`, polls until the fork is ready (GitHub forks are async), and opens PRs against any public repo specified in a component-to-repo map.

Security came next. [@enxebre](https://github.com/enxebre)'s [ai-helpers #590](https://github.com/openshift-eng/ai-helpers/pull/590) added an author authorization gate — a 248-line Python script that checks OWNERS files, approved bot lists, and GitHub org membership before allowing the agent to process review comments. Without this, any GitHub user could instruct the agent to make code changes by posting a review comment. Quality gates tightened too: [ai-helpers #618](https://github.com/openshift-eng/ai-helpers/pull/618) added an eval harness with 4 fixture-based test cases and 6 judges for the `jira:ready-to-solve` skill, running at roughly $2 per evaluation. Case 003 tests whether the AI catches vague acceptance criteria even when all deterministic structural checks pass.

[@bryan-cox](https://github.com/bryan-cox)'s [ai-helpers #629](https://github.com/openshift-eng/ai-helpers/pull/629) addressed a concrete problem: the jira-agent was not creating unit tests by default, and when it did, it ignored project-specific naming conventions. The fix added a dedicated unit test creation step requiring agents to check `TESTING.md`, `DEVELOPMENT.md`, and `CONTRIBUTING.md` before writing tests. The companion [hypershift #9036](https://github.com/openshift/hypershift/pull/9036) moved test conventions from a Claude skill into `TESTING.md` at the repo root, where both humans and agents reliably discover them.

Observability rounded out the month. [Release #81346](https://github.com/openshift/release/pull/81346) and [#81394](https://github.com/openshift/release/pull/81394) wired OTEL telemetry and BigQuery auto-data-loading into both the jira-agent and review-agent, establishing the pipeline: `agentic-ci` → OTEL JSONL → `extract_metrics.py` → autodl → BigQuery → Grafana.

!!! info "The Big Picture"
    The agentic CI pipeline is evolving from "an AI that opens PRs" into a production system with authorization gates, quality evaluation harnesses, observability, and multi-repo capability. The pattern is clear: automate the mechanical, gate the risky, measure everything.

---

## :material-lock-reset: Etcd Encryption Key Rotation and KMS Convergence

KMS key rotation on ARO HCP was silently broken for back-to-back rotations, and the root cause was one of those subtle Kubernetes behaviors that only bites you at scale.

[@muraee](https://github.com/muraee)'s [PR #8790](https://github.com/openshift/hypershift/pull/8790) added the API surface for tracking encryption key rotation — `SecretEncryptionStatus`, `SecretEncryptionKeyStatus`, and `EncryptionMigrationHistory` types with CEL validation rules replacing the older regex `Pattern` validators. This was a massive 121-file PR, though most of the diff was generated CRD manifests.

But the real drama was in the convergence logic. [@csrwng](https://github.com/csrwng) discovered in [PR #8970](https://github.com/openshift/hypershift/pull/8970) that `IsDeploymentReady` returns true while old KAS pods are still terminating. Kubernetes deployment status fields explicitly exclude terminating pods from their counters — they count "non-terminating" pods. So all status indicators look green while pods running the old encryption config are still serving requests. `ShouldPromoteTargetKey` fires prematurely, rewrites the encryption config, and triggers another KAS rollout. The result: three KAS pods with three different encryption configurations running simultaneously.

!!! danger "Production Impact"
    Back-to-back KMS key rotations on ARO HCP could result in three KAS pods with three different encryption configurations running simultaneously, risking data encrypted with a key that no remaining pod can decrypt.

The fix was elegant and fail-safe. An `encryption-config-hash` annotation on the KAS pod template lets both CPO and HCCO verify that the deployment actually rolled out with the specific encryption config containing the target key, not just that it looks ready from a previous rollout. A `hasTerminatingPods` helper lists pods matching the deployment's selector and returns false if any have `DeletionTimestamp` set. Critically, if pod listing fails for any reason, convergence is NOT reported — the system fails closed.

[@csrwng](https://github.com/csrwng) empirically verified the bug by adding a 30-second `preStop` hook to a single-replica deployment: `Status.Replicas` drops immediately while the old pod runs for 30 more seconds. The backport to release-4.22 ([#8972](https://github.com/openshift/hypershift/pull/8972)) was labeled critical severity.

---

## :material-microsoft-azure: Azure Platform Maturity Sprint

Azure self-managed HyperShift is racing toward GA, and this month saw a coordinated push across multiple workstreams to close the remaining gaps.

Scale-from-zero autoscaling arrived with [@jhjaggars](https://github.com/jhjaggars)' [PR #8337](https://github.com/openshift/hypershift/pull/8337), a 26-file PR that implemented an Azure instance type provider using the `armcompute.ResourceSKUsClient`, updated CEL validation to allow `autoScaling.min=0` for Azure alongside AWS, and generalized the scale-from-zero support checks across the nodepool controller. The Azure provider uses a region-wide SKU cache with a 1-hour TTL to avoid expensive per-request API calls.

[@bryan-cox](https://github.com/bryan-cox) delivered ExternalPrivateService support for the Azure PLS controller in [PR #8718](https://github.com/openshift/hypershift/pull/8718), enabling DNS-based private connectivity for Azure self-managed clusters. This brings Azure to parity with AWS and GCP private link controllers. The accompanying v2 e2e lifecycle test validates Private-to-PublicAndPrivate-to-Private topology transitions.

[@muraee](https://github.com/muraee)'s topology backport [PR #8721](https://github.com/openshift/hypershift/pull/8721) replaced the global `MANAGED_SERVICE` environment variable with per-cluster API fields in roughly 25 CPO files — a correctness fix for mixed management clusters hosting both ARO HCP and self-managed Azure clusters. [@jparrill](https://github.com/jparrill) added etcd backup infrastructure for self-managed Azure in [PR #8785](https://github.com/openshift/hypershift/pull/8785), including Azure Blob Storage setup scripts using Workload Identity with federated credentials.

The ARO HCP ingress fix ([#8992](https://github.com/openshift/hypershift/pull/8992) by [@bryan-cox](https://github.com/bryan-cox)) addressed a critical-severity bug where `PublicAndPrivate` topology on ARO HCP clusters caused the ingress operator to create per-cluster internal Azure load balancers, routing `*.apps` DNS to private IPs instead of the shared ingress public IP. The root cause: a single code path in `NewIngressParams()` that was correct for self-managed Azure but wrong for ARO HCP. The fix was a one-line `IsAroHCPByHCP` guard, validated on a live cluster by [@zgalor](https://github.com/zgalor).

---

## :material-linux: Dual-Stream RHEL: Preparing for OCP 5.0

OpenShift 5.0 will run on RHEL 10, but the migration from RHEL 9 cannot be a flag day. NodePools need to support both OS streams simultaneously, with the ability to select which stream a pool runs and roll between them safely. This month, [@sdminonne](https://github.com/sdminonne) landed the foundational controller plumbing across three PRs.

[PR #8730](https://github.com/openshift/hypershift/pull/8730) wired `osImageStream` into the NodePool controller, adding `GetRHELStream()` resolution logic (returning `rhel-9` for releases below 5.0 and `rhel-10` for 5.0+), integrating the stream into the config hash with normalization to avoid spurious rollouts, and threading the stream through boot image functions for both AWS and GCP. The hash normalization is particularly important: without it, setting the default stream explicitly (which is semantically identical to not setting it) would trigger a fleet-wide rollout.

[PR #8832](https://github.com/openshift/hypershift/pull/8832) extended OS stream awareness to all remaining platforms — Azure, PowerVS, OpenStack, and KubeVirt — adding `usesRuncRuntime()`, a function that scans ConfigMaps for `ContainerRuntimeConfig` entries specifying the runc runtime. The 363+ lines of new tests in `osstream_test.go` cover stream name resolution across all platforms.

[PR #8792](https://github.com/openshift/hypershift/pull/8792) completed the ignition layer by extending the `IgnitionProvider` interface with an `osStream` parameter, enabling `GetPayload` to generate OS-stream-specific ignition configs and write `OSImageStream` manifests.

!!! warning "Design Decision"
    Boot image resolution is currently hardcoded to `rhel-9` until MCO can install RHEL 10. The infrastructure is in place, but the actual switch waits for downstream readiness. Multiple `TODO` comments for `CNTRLPLANE-3553` track the remaining work.

---

## :material-bug-check: Production Bug Squashing

Some of the highest-impact PRs this month were fixes for bugs that had been silently degrading production clusters.

**Etcd peer TLS failures.** [@vsolanki12](https://github.com/vsolanki12)'s [PR #8479](https://github.com/openshift/hypershift/pull/8479) fixed intermittent etcd peer communication failures affecting approximately 50% of connection attempts in bare metal HCP deployments. The root cause was beautifully subtle: two headless services (etcd-client and etcd-discovery) selected the same pods, causing CoreDNS to register dual PTR records. Go's `getnameinfo()` non-deterministically returned the wrong one, and TLS verification failed when the certificate SAN didn't match. The fix was removing one line (`clusterIP: None`) from the etcd-client service YAML.

**MachineDeployment deadlock.** [@csrwng](https://github.com/csrwng)'s [PR #8821](https://github.com/openshift/hypershift/pull/8821) fixed a permanent NodePool rollout hang caused by stale CAPI `conversion-data` annotations — a v1alpha4-to-v1beta1 migration artifact that can become permanently stale. The fix replaced annotation-based checks with direct v1beta2 `Status` field checks, addressing a customer-impacting deadlock.

**KubeVirt EndpointSlice poisoning.** [@dpateriya](https://github.com/dpateriya)'s [PR #8892](https://github.com/openshift/hypershift/pull/8892) fixed ~50% intermittent LoadBalancer service connection timeouts on KubeVirt hosted clusters. After CAPK started exposing all VMI interface IPs for dual-stack CSR approval, the EndpointSlice generator was including non-routable OVN overlay IPs. Three customer cases confirmed the impact.

**Registry override matching.** [@muraee](https://github.com/muraee)'s [PR #8824](https://github.com/openshift/hypershift/pull/8824) fixed registry override matching for disconnected environments — `matchesPrefix` only accepted `:` as a separator, but digest-based image references use `@sha256:`. Without this fix, image pulls in air-gapped environments silently fell back to the public registry and failed.

**componentRoutes reconciliation failure.** [@reedcort](https://github.com/reedcort)'s [PR #8838](https://github.com/openshift/hypershift/pull/8838) fixed HostedCluster reconciliation failures when `componentRoutes` TLS secrets were configured. The controller was trying to sync secrets from the management cluster that are hosted-cluster-side resources. Labeled `rosa-impact`, this was directly affecting ROSA deployments.

---

## :material-refresh: HostedCluster Reconcile Loop Refactor

The HostedCluster reconciler had a structural problem: a missing SSH key could prevent HCP creation and operator deployments. Not because SSH keys are required for control plane operation, but because the reconcile loop processed operations sequentially, and any failure short-circuited everything downstream.

[@muraee](https://github.com/muraee)'s [PR #7908](https://github.com/openshift/hypershift/pull/7908) was the largest PR in this reporting period — 3,706 additions across 10 files — and it fundamentally restructured how the reconciler handles failures. The new architecture introduces a `reconcileReport` struct with `operationCategory` (critical vs. nonCritical) and `execute`/`executeOrBlock` methods. Operations are organized into phases: prerequisites (which still short-circuit), critical sync, non-critical sync, core HCP chain, and component deployment (which is blocked only if critical failures exist).

The `ReconciliationSucceeded` condition now reflects structured error reports with critical failure and blocked operation names, giving operators immediate visibility into what failed and what was blocked as a consequence. Non-critical errors are suppressed when critical failures exist, focusing attention on root causes rather than cascading symptoms.

!!! info "Production Safety"
    The PR includes a full rollback mechanism. The `ReconcileLegacy` flag and `reconcile_legacy.go` (1,806 lines) preserve the original reconcile implementation verbatim, activated by `HYPERSHIFT_RECONCILE_LEGACY=1`. This is production-grade engineering: a major architectural change with a one-environment-variable escape hatch.

---

## :material-robot: The Rise of Bot-Authored PRs

The `hypershift-jira-solve-ci` bot and `redhat-chai-bot` collectively authored a significant portion of the PRs merged this month. What's notable isn't just the volume — it's the nature of the work. These aren't trivial formatting fixes. They are solving real bugs, adding real tests, and shipping real features.

The router ordering fix ([#8971](https://github.com/openshift/hypershift/pull/8971)) by `hypershift-jira-solve-ci` was one of the most architecturally significant PRs of the month. The router's `adaptConfig` function listed existing Route objects to generate the HAProxy ConfigMap, but when registered as component #8, ignition-server (#35) and metrics-proxy (#38) hadn't created their routes yet. The initial HAProxy config missed ignition backends, routing ignition requests to the KAS default backend and failing NodePool ignition. The bot used a belt-and-suspenders approach: positional ordering plus `WithDependencies` via the component framework, with 417 lines of new tests.

The HCCO condition surfacing PR ([#8340](https://github.com/openshift/hypershift/pull/8340)) added `ConfigOperatorReconciliationSucceeded` as a new condition on HostedControlPlane and HostedCluster, providing parity with existing HO/CPO error reporting. The `--kubeconfig` flag PR ([#8402](https://github.com/openshift/hypershift/pull/8402)) threaded a kubeconfig path through 19 files touching both CLIs. The CRD breaking-change detection CI ([#8535](https://github.com/openshift/hypershift/pull/8535)) introduced a standalone Go tool that checks for field removals, type changes, new required fields, and enum changes against a base git commit.

The nil map panic fix ([#8924](https://github.com/openshift/hypershift/pull/8924)) caught a crash in `applyRequestsOverrides` when a resource-request-override annotation targeted a container without existing `Resources.Requests`. The safe-to-delete label PR ([#8961](https://github.com/openshift/hypershift/pull/8961)) added the `safe-to-delete-with-cluster` label to 5 CLI-created platform secrets across AWS, Azure, and GCP that were being orphaned on HostedCluster deletion.

The `redhat-chai-bot` contributed envtest input validation ([#9015](https://github.com/openshift/hypershift/pull/9015)), adding a closure that asserts exactly one of `expected`, `expectedError`, or `expectedStatusError` is non-empty in every `assertOnUpdate` test case, then fixed 14 YAML test suite files that violated the invariant. The OVN internal subnet fields PR ([#8249](https://github.com/openshift/hypershift/pull/8249)) added `V4InternalSubnet` and `V6InternalSubnet` to `OVNKubernetesConfig` with CEL validation and immutability rules — a 43-file PR solving a real problem where default OVN internal subnets overlap with customer network infrastructure.

!!! tip "Quality Infrastructure"
    The unit test enforcement ([ai-helpers #629](https://github.com/openshift-eng/ai-helpers/pull/629)), authorization gate ([#590](https://github.com/openshift-eng/ai-helpers/pull/590)), and eval harness ([#618](https://github.com/openshift-eng/ai-helpers/pull/618)) are infrastructure investments in making bot-authored code systematically reliable rather than hoping each individual PR happens to be correct.

---

## :material-star-shooting: Beneath the Headlines

!!! info "254 PRs, 8 stories — what about the rest?"
    The stories above account for fewer than 40 of the 254 merged PRs. Here is a sampling of the other significant work that landed during this period.

**TLS security profile enforcement** was a cross-cutting theme. [@ricardomaraschini](https://github.com/ricardomaraschini) propagated TLS settings to aws-pod-identity-webhook, azure-workload-identity-webhook ([#8713](https://github.com/openshift/hypershift/pull/8713)), control-plane-pki-operator ([#8768](https://github.com/openshift/hypershift/pull/8768)), and configured etcd to respect the cluster TLS profile ([#8871](https://github.com/openshift/hypershift/pull/8871)) — a 253-file PR where 248 files were auto-generated ModernTLS test fixtures. [@tmshort](https://github.com/tmshort) injected TLS flags into packageserver ([#9001](https://github.com/openshift/hypershift/pull/9001)). Together, these close compliance gaps for post-quantum cryptography readiness.

**CI reliability improvements** were everywhere. [@jparrill](https://github.com/jparrill) added retry logic for AWS CreateVpcEndpoint throttle errors ([#9012](https://github.com/openshift/hypershift/pull/9012)), fixed stale DaemonSet cleanup in the GlobalPullSecret test ([#8990](https://github.com/openshift/hypershift/pull/8990)), and built a comprehensive `cleanleaked` Go tool ([#8964](https://github.com/openshift/hypershift/pull/8964)) for safe AWS CI resource cleanup — replacing bash scripts that caused a multi-hour CI outage on July 6 by accidentally deleting NAT gateways. [@bryan-cox](https://github.com/bryan-cox) fixed bastion KeyPair leaks ([#8982](https://github.com/openshift/hypershift/pull/8982)) that had been accumulating since May 11, hitting the AWS 5,000 KeyPair limit on June 17.

**Pre-commit hook restructuring** by [@bryan-cox](https://github.com/bryan-cox) ([#9042](https://github.com/openshift/hypershift/pull/9042), [#9016](https://github.com/openshift/hypershift/pull/9016)) reduced push hook runtime from 15+ minutes to 3-8 minutes, unblocking both developer local workflows and AI-assisted development which had a 10-minute timeout.

**Monitoring and metrics.** [@muraee](https://github.com/muraee)'s [#8626](https://github.com/openshift/hypershift/pull/8626) added `spec.monitoring` API types to HostedCluster and HostedControlPlane, replacing annotation-based metrics forwarding with proper spec fields.

**Developer experience.** [@ironcladlou](https://github.com/ironcladlou) made CLI credential flags optional ([#8889](https://github.com/openshift/hypershift/pull/8889)), falling back to the AWS SDK default credential chain. [@csrwng](https://github.com/csrwng) added a `create operator-roles aws` command ([#8636](https://github.com/openshift/hypershift/pull/8636)) for least-privilege IAM roles. [@devguyio](https://github.com/devguyio) reintroduced the `verify-ci` Make target ([#9045](https://github.com/openshift/hypershift/pull/9045)) after stale ModernTLS fixtures broke main.

**Platform-specific fixes.** [@mihivagyok](https://github.com/mihivagyok) fixed oauth-server masterURL to use in-cluster DNS ([#8772](https://github.com/openshift/hypershift/pull/8772)), resolving a 100% token display failure on IBM Cloud. [@cristianoveiga](https://github.com/cristianoveiga) removed the obsolete `ClusterResourceSet=false` feature gate from CAPG ([#8795](https://github.com/openshift/hypershift/pull/8795)), fixing a crash-loop on GCP for OCP 4.22+.

**Nightly release gating.** [@Nirshal](https://github.com/Nirshal)'s [#8602](https://github.com/openshift/hypershift/pull/8602) added a complete Tekton-based release gate pipeline for HyperShift Operator images targeting ARO HCP — the largest PR by line count (3,794 lines), with all Python code using stdlib only to maintain hermetic Konflux builds.

**The Karpenter separation begins.** [@maxcao13](https://github.com/maxcao13) added a `KarpenterOperator` feature gate ([#8976](https://github.com/openshift/hypershift/pull/8976)) behind TechPreviewNoUpgrade — currently a no-op, but the first step toward extracting karpenter-operator from HyperShift into a standalone component.

---

## :material-chart-bar: By the Numbers

| Metric | Value |
|--------|-------|
| Total PRs merged | 254 |
| Unique contributors | 57 |
| Bot PRs | 34 |
| HyperShift repo PRs | 157 |
| Release repo PRs | 69 |
| AI-helpers repo PRs | 23 |
| Enhancement proposals | 5 |
| Average merge time | 300.6 hours |
| Customer-reported fixes | 18 |

**Top Reviewers**

| Reviewer | PRs Reviewed |
|----------|-------------|
| [@bryan-cox](https://github.com/bryan-cox) | 65 |
| [@jparrill](https://github.com/jparrill) | 36 |
| [@enxebre](https://github.com/enxebre) | 32 |
| [@csrwng](https://github.com/csrwng) | 20 |
| [@cblecker](https://github.com/cblecker) | 13 |

---

## :octicons-people-24: Contributors

Click any column header to sort. Each number links to the contributor's PRs in that repository.

| Contributor | hypershift | release | ai-helpers | enhancements | :material-bug: bugs | Total |
|------------|:-:|:-:|:-:|:-:|:-:|:-:|
| [@bryan-cox](https://github.com/bryan-cox) | [22](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-06-23..2026-07-22) | [5](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-06-23..2026-07-22+hypershift) | [6](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-06-23..2026-07-22) |  | [8](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **33** |
| [@enxebre](https://github.com/enxebre) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-06-23..2026-07-22) | [18](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-06-23..2026-07-22+hypershift) | [9](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-06-23..2026-07-22) |  | [7](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **29** |
| [@mgencur](https://github.com/mgencur) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-06-23..2026-07-22) | [8](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-06-23..2026-07-22+hypershift) |  |  | [1](https://github.com/openshift/hypershift/pull/8805) | **12** |
| [@celebdor](https://github.com/celebdor) | [9](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-06-23..2026-07-22) | [1](https://github.com/openshift/release/pull/81725) |  |  | [4](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **10** |
| [@csrwng](https://github.com/csrwng) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acsrwng+merged%3A2026-06-23..2026-07-22) | [1](https://github.com/openshift/release/pull/80883) |  |  | [6](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Acsrwng+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **9** |
| [@jparrill](https://github.com/jparrill) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-06-23..2026-07-22) | [1](https://github.com/openshift/release/pull/81729) |  |  | [5](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **9** |
| [@muraee](https://github.com/muraee) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amuraee+merged%3A2026-06-23..2026-07-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Amuraee+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **8** |
| [@vsolanki12](https://github.com/vsolanki12) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-06-23..2026-07-22) |  |  |  | [7](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **8** |
| [@devguyio](https://github.com/devguyio) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adevguyio+merged%3A2026-06-23..2026-07-22) | [5](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Adevguyio+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **7** |
| [@ironcladlou](https://github.com/ironcladlou) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-06-23..2026-07-22) | [1](https://github.com/openshift/release/pull/81878) |  |  | [1](https://github.com/openshift/hypershift/pull/8883) | **5** |
| [@ricardomaraschini](https://github.com/ricardomaraschini) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aricardomaraschini+merged%3A2026-06-23..2026-07-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8245) | **5** |
| [@yiraeChristineKim](https://github.com/yiraeChristineKim) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3AyiraeChristineKim+merged%3A2026-06-23..2026-07-22) |  |  |  | [5](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3AyiraeChristineKim+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **5** |
| [@hector-vido](https://github.com/hector-vido) |  | [4](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ahector-vido+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **4** |
| [@reedcort](https://github.com/reedcort) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Areedcort+merged%3A2026-06-23..2026-07-22) |  |  |  | [4](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Areedcort+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **4** |
| [@sdminonne](https://github.com/sdminonne) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Asdminonne+merged%3A2026-06-23..2026-07-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8536) | **4** |
| [@Nirshal](https://github.com/Nirshal) | [1](https://github.com/openshift/hypershift/pull/8602) | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3ANirshal+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **3** |
| [@jhjaggars](https://github.com/jhjaggars) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajhjaggars+merged%3A2026-06-23..2026-07-22) |  |  | [1](https://github.com/openshift/enhancements/pull/1979) |  | **3** |
| [@maxcao13](https://github.com/maxcao13) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amaxcao13+merged%3A2026-06-23..2026-07-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Amaxcao13+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **3** |
| [@mtulio](https://github.com/mtulio) | [1](https://github.com/openshift/hypershift/pull/8835) | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amtulio+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **3** |
| [@stephenfin](https://github.com/stephenfin) | [1](https://github.com/openshift/hypershift/pull/8687) | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Astephenfin+merged%3A2026-06-23..2026-07-22+hypershift) |  |  | [1](https://github.com/openshift/hypershift/pull/8687) | **3** |
| [@PoornimaSingour](https://github.com/PoornimaSingour) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-06-23..2026-07-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **2** |
| [@amasolov](https://github.com/amasolov) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aamasolov+merged%3A2026-06-23..2026-07-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aamasolov+merged%3A2026-06-23..2026-07-22+OCPBUGS&type=pullrequests) | **2** |
| [@cristianoveiga](https://github.com/cristianoveiga) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acristianoveiga+merged%3A2026-06-23..2026-07-22) |  |  |  |  | **2** |
| [@neisw](https://github.com/neisw) |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aneisw+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **2** |
| [@orenc1](https://github.com/orenc1) |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aorenc1+merged%3A2026-06-23..2026-07-22+hypershift) |  |  |  | **2** |
| [@patjlm](https://github.com/patjlm) | [1](https://github.com/openshift/hypershift/pull/8863) | [1](https://github.com/openshift/release/pull/81220) |  |  |  | **2** |
| [@twolff-gh](https://github.com/twolff-gh) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Atwolff-gh+merged%3A2026-06-23..2026-07-22) |  |  |  |  | **2** |
| [@BraeTroutman](https://github.com/BraeTroutman) | [1](https://github.com/openshift/hypershift/pull/8888) |  |  |  |  | **1** |
| [@DavidHurta](https://github.com/DavidHurta) | [1](https://github.com/openshift/hypershift/pull/8013) |  |  |  |  | **1** |
| [@JoelSpeed](https://github.com/JoelSpeed) | [1](https://github.com/openshift/hypershift/pull/8980) |  |  |  |  | **1** |
| [@Sandeepyadav93](https://github.com/Sandeepyadav93) | [1](https://github.com/openshift/hypershift/pull/8850) |  |  |  |  | **1** |
| [@apahim](https://github.com/apahim) |  | [1](https://github.com/openshift/release/pull/81876) |  |  |  | **1** |
| [@cssjr](https://github.com/cssjr) | [1](https://github.com/openshift/hypershift/pull/8613) |  |  |  |  | **1** |
| [@dhgautam99](https://github.com/dhgautam99) | [1](https://github.com/openshift/hypershift/pull/8748) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8748) | **1** |
| [@dpateriya](https://github.com/dpateriya) | [1](https://github.com/openshift/hypershift/pull/8892) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8892) | **1** |
| [@ehearne-redhat](https://github.com/ehearne-redhat) | [1](https://github.com/openshift/hypershift/pull/8287) |  |  |  |  | **1** |
| [@gangwgr](https://github.com/gangwgr) |  | [1](https://github.com/openshift/release/pull/80407) |  |  |  | **1** |
| [@georgelipceanu](https://github.com/georgelipceanu) | [1](https://github.com/openshift/hypershift/pull/8830) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8830) | **1** |
| [@hongkailiu](https://github.com/hongkailiu) |  |  |  | [1](https://github.com/openshift/enhancements/pull/2045) |  | **1** |
| [@jhadvig](https://github.com/jhadvig) | [1](https://github.com/openshift/hypershift/pull/8839) |  |  |  |  | **1** |
| [@jiezhao16](https://github.com/jiezhao16) |  | [1](https://github.com/openshift/release/pull/80098) |  |  |  | **1** |
| [@kaleemsiddiqu](https://github.com/kaleemsiddiqu) | [1](https://github.com/openshift/hypershift/pull/8793) |  |  |  |  | **1** |
| [@mdbooth](https://github.com/mdbooth) |  |  |  | [1](https://github.com/openshift/enhancements/pull/1918) |  | **1** |
| [@mfbonfigli](https://github.com/mfbonfigli) | [1](https://github.com/openshift/hypershift/pull/8401) |  |  |  |  | **1** |
| [@mihivagyok](https://github.com/mihivagyok) | [1](https://github.com/openshift/hypershift/pull/8772) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8772) | **1** |
| [@patilsuraj767](https://github.com/patilsuraj767) | [1](https://github.com/openshift/hypershift/pull/8296) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8296) | **1** |
| [@pskrbasu](https://github.com/pskrbasu) |  | [1](https://github.com/openshift/release/pull/81533) |  |  |  | **1** |
| [@rutvik23](https://github.com/rutvik23) |  | [1](https://github.com/openshift/release/pull/80145) |  |  | [1](https://github.com/openshift/release/pull/80145) | **1** |
| [@shubhadapaithankar](https://github.com/shubhadapaithankar) | [1](https://github.com/openshift/hypershift/pull/8725) |  |  |  |  | **1** |
| [@shwetam2004](https://github.com/shwetam2004) |  | [1](https://github.com/openshift/release/pull/80895) |  |  | [1](https://github.com/openshift/release/pull/80895) | **1** |
| [@stevekuznetsov](https://github.com/stevekuznetsov) | [1](https://github.com/openshift/hypershift/pull/8804) |  |  |  |  | **1** |
| [@tchap](https://github.com/tchap) |  |  |  | [1](https://github.com/openshift/enhancements/pull/2015) |  | **1** |
| [@tmshort](https://github.com/tmshort) | [1](https://github.com/openshift/hypershift/pull/9001) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9001) | **1** |
| [@vismishr](https://github.com/vismishr) | [1](https://github.com/openshift/hypershift/pull/8560) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8560) | **1** |
| [@wanghaoran1988](https://github.com/wanghaoran1988) | [1](https://github.com/openshift/hypershift/pull/8715) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8715) | **1** |
| [@wking](https://github.com/wking) |  |  |  | [1](https://github.com/openshift/enhancements/pull/1928) |  | **1** |

---

## :material-crystal-ball: What's Next

The work merged in this period sets up several major threads for the coming months.

**Etcd sharding scale validation.** The sharding infrastructure is behind a TechPreview gate. The next phase is scale testing with the Performance & Scale team to validate the design under realistic high-node, high-activity workloads.

**Azure self-managed GA.** Scale-from-zero, ExternalPrivateService, topology per-cluster fields, and etcd backup infrastructure all landed this month. The remaining gaps — shared ingress refinements, end-to-end topology transition testing, and the final compliance audit — are the last stretch before GA readiness.

**Agentic CI maturity.** With authorization gates, quality evals, OTEL telemetry, and cross-repo capabilities in place, the next phase is expanding the component-to-repo map beyond `openshift/installer`, tuning the eval judges from production feedback, and using the BigQuery/Grafana pipeline to identify which types of Jira issues the agents solve well vs. poorly.

**OCP 5.0 dual-stream activation.** The controller plumbing for all platforms is complete with stream-aware boot image resolution and ignition support. The actual RHEL 10 activation waits for MCO readiness (`CNTRLPLANE-3553`), followed by e2e validation and promotion from TechPreview.

**Karpenter extraction.** The `KarpenterOperator` feature gate is a no-op today, but it marks the beginning of extracting karpenter-operator from HyperShift into a standalone component at `openshift/karpenter-operator` — simplifying the HyperShift dependency tree and enabling independent release cadences.

The pace is not slowing down.
