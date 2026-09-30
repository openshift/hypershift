---
title: August 2026 Progress Report
description: OSStreams graduates to Default, CAPI v1beta2 storage migration, Karpenter goes standalone, a custom HyperShift linter, Azure Managed HSM, e2e v2 framework maturity, upsert desired-state hash fix, and a massive K8s dependency bump — 279 PRs from 65 contributors.
---

# August 2026 Progress Report

<div class="grid cards" markdown>

-   :octicons-git-pull-request-24:{ .lg } **279** PRs merged
-   :octicons-people-24:{ .lg } **65** contributors
-   :octicons-alert-24:{ .lg } **0** breaking changes
-   :octicons-clock-24:{ .lg } **240.6h** avg merge time

</div>

HyperShift's July 23 to August 22 window was defined by culminations. Features that had been building across multiple months — OSStreams, CAPI v1beta2 migration, the Karpenter extraction — all reached significant milestones. Meanwhile, a custom static analysis tool started enforcing project conventions at lint time, a fundamental upsert bug that had been hiding in plain sight for years finally got fixed, and the e2e v2 framework crossed the threshold from "promising experiment" to "primary test infrastructure for AWS."

Sixty-five contributors participated across the hypershift, release, enhancements, and ai-helpers repositories. Fifty-four of the 279 merged PRs came from bots — cherry-picks, dependency bumps, CVE remediations, and Jira-driven fixes. Ten PRs addressed customer-reported bugs. The average time from PR open to merge was 240.6 hours, down from 300.6 last month, reflecting both faster review cycles and a higher proportion of well-scoped, incremental PRs.

What follows are eight stories from this period, chosen because they best illustrate the engineering challenges and the design decisions that shaped them.

---

## :material-linux: OSStreams Graduates to Default

OpenShift 5.0 runs on RHEL 10. But you can't flip an entire fleet from RHEL 9 to RHEL 10 in one shot — NodePools need to declare which OS stream they run, and the system needs to resolve the correct boot images, ignition configs, and runtime handlers accordingly. The `OSStreams` feature gate has been building toward this for months, and this month it graduated from TechPreview to Default.

[@jparrill](https://github.com/jparrill)'s [PR #9099](https://github.com/openshift/hypershift/pull/9099) was the centerpiece — an 814-line PR across 20 files that promoted the feature gate and wired dynamic RHEL stream resolution into the boot image path. Previously, the stream was hardcoded or resolved statically at NodePool creation time. Now, on upgrade, `resolveRHELStreamFromRelease()` inspects the target OCP release version and returns `rhel-9` for pre-5.0 and `rhel-10` for 5.0+. This matters for a specific scenario: a cluster upgrading from 4.x to 5.0 needs its NodePools to detect that the target release requires RHEL 10 boot images, even though the pool was originally created against RHEL 9.

The e2e coverage was substantial. [@sdminonne](https://github.com/sdminonne)'s [PR #9033](https://github.com/openshift/hypershift/pull/9033) added 426 lines of v2 OSImageStream tests covering stream selection, upgrade behavior, and immutability constraints. [@jparrill](https://github.com/jparrill)'s follow-up [PR #9297](https://github.com/openshift/hypershift/pull/9297) retagged the tests for the new ginkgo label structure, and [@csrwng](https://github.com/csrwng)'s [PR #9206](https://github.com/openshift/hypershift/pull/9206) cleaned up a test that was violating osImageStream immutability by attempting to remove the field after setting it.

A subtle bug surfaced during the rollout. [@bennerv](https://github.com/bennerv)'s [PR #9283](https://github.com/openshift/hypershift/pull/9283) fixed the ignition server to use the correct `v1` machine-config API for `OSImageStream` manifests — the wrong API version was causing silent failures during ignition payload generation. Meanwhile, [@bryan-cox](https://github.com/bryan-cox)'s [PR #9115](https://github.com/openshift/hypershift/pull/9115) fixed the OSImageStream e2e test itself, which was broken for OCP 5.0 because the `Makefile` test-changed target didn't account for the new version numbering scheme.

The version bump to OCP 5.1 ([PR #9288](https://github.com/openshift/hypershift/pull/9288) by [@Nirshal](https://github.com/Nirshal)) completed the picture — the supported version ceiling moved up, and the stream resolution logic now handles three generations of release versions correctly.

!!! tip "Key Takeaway"
    The OSStreams graduation means every HyperShift deployment now has dual-stream RHEL support enabled by default. NodePools created against RHEL 9 will automatically resolve RHEL 10 boot images when upgraded to OCP 5.0+. The feature is no longer opt-in.

---

## :material-database-sync: CAPI v1beta2 Storage Version Migration

Cluster API shipped v1beta2 as the new storage version, but HyperShift clusters have been running with CAPI objects stored as v1alpha4 and v1beta1 in etcd for years. The Kubernetes API server can serve any version via conversion webhooks, but the old storage versions create an invisible risk: if a future CAPI release drops support for v1alpha4 serving, every unconverted object becomes unreadable. The storage version migration needed to happen proactively, and it needed to be safe.

[@clebs](https://github.com/clebs)'s [PR #8938](https://github.com/openshift/hypershift/pull/8938) landed a comprehensive 2,338-line migration system adapted from upstream CAPI's own `crd_migrator.go`. The migrator operates in two phases. First, `StorageVersionMigrationPhase` re-reads and re-writes every custom resource so the API server stores it in the current storage version. Second, `CleanupManagedFieldsPhase` removes `managedFields` entries that reference API versions no longer served — these stale entries cause spurious conflict errors during server-side apply because the API server can't resolve the old version.

The design is deliberately conservative. The migrator watches CRD objects, compares `metadata.generation` against a `crd-migration.cluster.x-k8s.io/observed-generation` annotation, and only triggers migration when the CRD spec changes. A TTL cache prevents re-processing objects that were recently migrated. Each object is patched with `retry.RetryOnConflict` to handle concurrent modifications. The migrator uses the API reader (bypassing the cache) for listing objects to ensure it sees the actual stored version, not a cached conversion.

The companion [PR #9145](https://github.com/openshift/hypershift/pull/9145) added a `CAPIMigration` feature gate placeholder — currently a no-op that controls when the migration runs. The gate exists so the migration can be rolled out gradually across fleet management clusters rather than firing simultaneously everywhere.

The PR included a complete how-to guide at `docs/content/how-to/capi-storage-migration.md`, 432 lines of unit tests covering edge cases like CRDs with no custom resources and objects with mixed managed field versions, and a 263-line e2e test that validates the full migration lifecycle.

!!! info "Why This Matters"
    Without this migration, upgrading CAPI beyond v1beta2 in the future would risk data loss — objects stored in v1alpha4 would become unreadable if that version's conversion webhook is removed. The migration eliminates that risk proactively, one object at a time, with per-object conflict retry and a feature gate for controlled rollout.

---

## :material-kubernetes: Karpenter Operator Goes Standalone

Until this month, the Karpenter operator was embedded inside HyperShift — deployed as part of the HyperShift operator's control plane and sharing its release lifecycle. That tight coupling was becoming a problem. Karpenter's release cadence doesn't match HyperShift's. Bug fixes in the autoscaler shouldn't require a full HyperShift operator release. And teams working on Karpenter shouldn't need to navigate HyperShift's sprawling codebase to deploy a test build.

[@maxcao13](https://github.com/maxcao13)'s [PR #9245](https://github.com/openshift/hypershift/pull/9245) added the deployment path for a standalone karpenter-operator — a separate `Deployment` resource in the HyperShift operator namespace, with its own service account, RBAC, and lifecycle. The 405-line PR wired up the deployment creation in the HyperShift operator's reconcile loop, gated behind the `KarpenterOperator` feature flag that was introduced in the previous reporting period. When enabled, the HyperShift operator creates a karpenter-operator Deployment instead of managing Karpenter directly.

The follow-up [PR #9295](https://github.com/openshift/hypershift/pull/9295) added a dev image annotation override, letting developers point the karpenter-operator Deployment at a custom image without modifying the operator code. This is the same pattern HyperShift uses for CPO image overrides — an annotation on the HostedCluster that the operator reads during reconciliation.

[@maxcao13](https://github.com/maxcao13) also bumped the Karpenter dependencies to v1.13.0 in [PR #9170](https://github.com/openshift/hypershift/pull/9170) — a 3,576-line vendor update — and ported the core Karpenter autonode tests to the v2 e2e framework. [@fishereskew](https://github.com/fishereskew)'s [PR #9060](https://github.com/openshift/hypershift/pull/9060) fixed a `yq` dependency issue that was breaking upstream Karpenter test execution.

!!! warning "Feature Gate Status"
    The standalone deployment path is behind `KarpenterOperator` in TechPreviewNoUpgrade. The feature gate controls whether the HyperShift operator creates the standalone Deployment or continues managing Karpenter inline. The inline path remains the default and will until the standalone mode has been validated at scale.

---

## :material-code-tags-check: The HyperShift Linter

Every large project accumulates conventions that live in people's heads — "test cases should be named this way," "don't use `context.Background()` in controller code," "e2e tests shouldn't call guest cluster APIs without the helper." These conventions get enforced in code review, which means they get enforced inconsistently, and reviewers spend cognitive bandwidth on things a machine should catch.

[@bryan-cox](https://github.com/bryan-cox)'s [PR #9237](https://github.com/openshift/hypershift/pull/9237) introduced `hypershiftlinter`, a custom `golangci-lint` plugin built as a standalone Go module under `hack/tools/`. The plugin framework wraps ten static analysis analyzers, each targeting a specific project convention:

- **`testcasename`** enforces that test case `name` fields match the pattern `"When <condition>, it should <expected behavior>"` — a convention from `TESTING.md` that was previously enforced only in review.
- **`testfuncname`** checks that test function names follow `Test<Type>_<Method>` conventions.
- **`contextbackground`** flags uses of `context.Background()` in production code where a passed context should be used instead.
- **`guestcluster`** catches direct guest cluster API calls in e2e tests that bypass the framework's state management helpers.
- **`hcpstatuspatch`** detects raw status patches on HostedControlPlane that should go through the statuspatching helper.
- **`ipv6url`** catches unbracketed IPv6 addresses in URL construction — the same class of bug that [PR #9120](https://github.com/openshift/hypershift/pull/9120) fixed manually this month.
- **`sippyannotation`** validates Sippy test annotation formatting.
- **`e2eutilallowlist`** controls which packages can import internal e2e utilities.
- **`vacuouspass`** detects test functions that always pass without actually testing anything.
- **`contextbackground`** is separate from `guestcluster` because the fix is different: one requires threading a context, the other requires using a framework method.

The follow-up [PR #9271](https://github.com/openshift/hypershift/pull/9271) enabled the plugin project-wide in a single sweep, which naturally surfaced existing violations. [@rutvik23](https://github.com/rutvik23)'s [PR #9367](https://github.com/openshift/hypershift/pull/9367) and [@bryan-cox](https://github.com/bryan-cox)'s [PR #9351](https://github.com/openshift/hypershift/pull/9351) cleaned up the lint failures, renaming test cases to match the `When..., it should...` pattern across the codebase.

The plugin runs in CI as part of the standard lint workflow, with a separate test workflow ([PR #9305](https://github.com/openshift/hypershift/pull/9305)) that validates the linter's own test fixtures. The linter itself has tests — `plugin_test.go` runs each analyzer against known-good and known-bad fixtures to ensure the analyzers don't produce false positives.

!!! tip "Design Choice"
    Building a golangci-lint plugin rather than a standalone tool means the linter integrates into existing developer workflows — `golangci-lint run` in your editor catches violations immediately, before you push. The plugin architecture also means individual analyzers can be disabled per-package via `//nolint:testcasename` when exceptions are justified.

---

## :material-shield-key: Azure Managed HSM for KMS Encryption

Azure Key Vault comes in two flavors: Standard/Premium vaults and Managed HSM. Managed HSM provides FIPS 140-2 Level 3 validated hardware security modules — a hard requirement for certain compliance postures. But HyperShift's Azure KMS integration only supported Standard vaults. The code assumed a vault URL format (`https://<name>.vault.azure.net`), and the API had no field to distinguish vault types.

[@hlipsig](https://github.com/hlipsig)'s [PR #9199](https://github.com/openshift/hypershift/pull/9199) was a 79-file, 3,609-line PR that threaded Managed HSM support through the entire stack. The changes spanned three layers:

**API layer.** New `AzureKMSVaultType` enum with `StandardVault` and `ManagedHSM` values, added to `AzureKMSSpec`. CEL validation rules enforce that the vault type matches the key URL format — Managed HSM key URLs use `.managedhsm.azure.net` while Standard vaults use `.vault.azure.net`.

**CLI layer.** The `create cluster azure` command auto-detects vault type from the provided key URL. If the URL contains `.managedhsm.azure.net`, it sets `VaultType: ManagedHSM` without requiring an explicit flag. This is a nice UX touch — users don't need to know about the API field if they're providing a real key URL.

**Control plane operator.** KMS configuration generation in the CPO now produces different etcd encryption configs for each vault type. Managed HSM keys use a different URI path structure and different Azure SDK client calls.

[@vsolanki12](https://github.com/vsolanki12)'s companion [PR #8965](https://github.com/openshift/hypershift/pull/8965) fixed a related issue: KMS validation was failing for private Key Vaults on ARO HCP because the operator couldn't reach the vault endpoint from the management cluster. The fix adds an `IsAroHCPByHCP()` check to skip validation when the hosted control plane is ARO HCP-managed — the assumption being that ARO HCP's own infrastructure guarantees the vault is reachable from the hosted cluster's network.

---

## :material-test-tube: E2E v2 Framework Matures

The v2 e2e test framework has been under development since earlier this year, but this month it crossed a critical threshold: AWS lifecycle tests are now running in CI, producing real JUnit results, and catching real bugs.

[@ironcladlou](https://github.com/ironcladlou)'s [PR #9174](https://github.com/openshift/hypershift/pull/9174) wired up the first AWS v2 e2e lifecycle test coverage — 397 lines across 9 files that establish the test's cluster creation, mutation, and destruction lifecycle. Unlike v1 tests that create a fresh cluster per test function, v2 tests share a single cluster and compose assertions around lifecycle phases. This dramatically reduces cloud spend and test wall time, but requires careful state management to prevent test pollution.

That state management was the focus of [PR #9198](https://github.com/openshift/hypershift/pull/9198) and [PR #9229](https://github.com/openshift/hypershift/pull/9229). The first improved guest cluster state management reliability — ensuring that the test framework properly tracks which resources exist in the guest cluster and doesn't assert against stale state after mutations. The second tackled test isolation, with 559 lines of changes refactoring how tests declare their dependencies on cluster state.

One of the more subtle improvements was [PR #9168](https://github.com/openshift/hypershift/pull/9168), which added lifecycle-aware JUnit emission for informing tests. The problem: when a v2 test is marked as "informing" (not blocking), its JUnit results need to be emitted differently so that CI dashboards show the signal without gating merges. The 434-line PR added a custom JUnit reporter that tags informing test results with metadata, plus 275 lines of tests for the reporter itself.

[@ironcladlou](https://github.com/ironcladlou) also wrote comprehensive architectural documentation in [PR #9151](https://github.com/openshift/hypershift/pull/9151) — 1,713 lines including a detailed test flow document explaining the lifecycle phases, state management model, and assertion patterns. This is the kind of documentation that determines whether a framework gets adopted or abandoned: without it, every new test author rediscovers the design constraints by trial and error.

The linting integration came via [PR #9271](https://github.com/openshift/hypershift/pull/9271), where the new `guestcluster` analyzer catches direct guest cluster API calls that bypass the framework's state management — exactly the kind of mistake that causes flaky tests in a shared-cluster model.

---

## :material-fingerprint: Upsert Desired-State Hash

This one had been hiding in plain sight for years. HyperShift's `ApplyManifest` function — the core upsert logic that reconciles every control plane component — uses `DeepDerivative` to compare the desired state against the existing object. If they're equal, it skips the update. Simple, correct, and subtly broken.

`DeepDerivative` treats nil/zero/empty as "I don't care about this field." If the desired state has `nodeSelector: nil` and the existing object has `nodeSelector: {"key": "value"}`, `DeepDerivative` says they're equal. This is by design — it's meant for partial updates where you only specify the fields you care about. But HyperShift's upsert is supposed to be a full reconciliation: if a field is removed from the desired manifest, it should be removed from the existing object.

[@muraee](https://github.com/muraee)'s [PR #7713](https://github.com/openshift/hypershift/pull/7713) fixed this with a `DesiredStateHashAnnotation` — a SHA-256 hash of the desired manifest stored as an annotation on each managed object. On each reconciliation, the controller computes the hash of the desired state and compares it to the stored hash. If they differ, the update proceeds regardless of what `DeepDerivative` says. If they're equal, the `DeepDerivative` check still runs as an optimization to avoid unnecessary API calls.

The implementation was careful about what gets hashed. The `computeDesiredHash` function strips metadata (annotations, labels, resourceVersion, etc.) and status before hashing, so that controller-added metadata doesn't cause spurious updates. Only the spec-level fields that the controller "owns" contribute to the hash.

The PR touched 1,199 files — but 1,176 of those were test fixture updates adding the new annotation to expected YAML outputs. The actual logic change was 109 lines in `support/upsert/apply.go` and 296 lines of new tests in `apply_test.go`. The test coverage includes specific cases for field removal: adding a `nodeSelector`, verifying it's applied, removing the `nodeSelector` from the desired manifest, and verifying the update fires.

!!! danger "Production Impact"
    Before this fix, removing a `nodeSelector`, `toleration`, or trailing container argument from a CPO component manifest had no effect — the field remained on the live object indefinitely. The only workaround was manually deleting the object and letting the controller recreate it. This affected every platform and every component managed through the upsert path.

---

## :material-package-up: K8s v0.36.2 / CAPI v1.12.8 Bump

Major dependency bumps are the kind of PR that nobody wants to review but everybody benefits from. [@bryan-cox](https://github.com/bryan-cox)'s [PR #8695](https://github.com/openshift/hypershift/pull/8695) updated Kubernetes client libraries to v0.36.2, controller-runtime to v0.24.1, and Cluster API to v1.12.8. The PR touched 2,670 files with 216,512 insertions and 163,502 deletions — almost entirely vendored dependencies.

The Kubernetes 1.36 libraries bring updated API types, new admission policy support, and deprecation removals that downstream components depend on. Controller-runtime v0.24.1 includes performance improvements to the informer cache and fixes for server-side apply corner cases. CAPI v1.12.8 is the version that includes the v1beta2 storage version changes that the CAPI migration PR (#8938) depends on.

The bump wasn't just a `go get && go mod vendor`. Several API changes required code modifications: deprecated fields removed, interface signatures changed, and new required parameters added. [@JoelSpeed](https://github.com/JoelSpeed)'s [PR #9231](https://github.com/openshift/hypershift/pull/9231) followed up with an openshift-api bump to resolve broken integration tests on K8s 1.30 compatibility, and [@hlipsig](https://github.com/hlipsig)'s [PR #9223](https://github.com/openshift/hypershift/pull/9223) fixed the `hack/tools` Go version to match the main module after the bump changed the minimum Go version to 1.26.

The envtest infrastructure also needed updates. [@clebs](https://github.com/clebs)'s [PR #9215](https://github.com/openshift/hypershift/pull/9215) triggered envtest workflows on dependency changes, and new Kubernetes 1.36 envtest matrix entries were added to the GitHub Actions configuration to ensure test coverage against the new API server version.

---

## :material-star-shooting: Beneath the Headlines

!!! info "279 PRs, 8 stories — what about the rest?"
    The stories above cover fewer than 40 of the 279 merged PRs. Here is a sampling of the other significant work that landed during this period.

**AWS resource tag override policy** arrived with [@michaelryanmcneill](https://github.com/michaelryanmcneill)'s [PR #9152](https://github.com/openshift/hypershift/pull/9152). Previously, AWS resource tags were all-or-nothing: managed services could set tags, but customers could override any of them. The new per-tag `overridePolicy` field lets managed services protect specific tags (like billing or compliance identifiers) while allowing customer overrides on others. The API addition includes CEL validation to prevent conflicting policies on the same tag key.

**Documentation migrated to Zensical.** [@celebdor](https://github.com/celebdor)'s [PR #9139](https://github.com/openshift/hypershift/pull/9139) replaced mkdocs-material with [Zensical](https://zensical.com) — a 69-file PR that swapped the Python dependency manager from pip with `requirements.txt` to UV with `uv.lock`, added `pyproject.toml` for declarative configuration, and updated the build pipeline. Follow-up PRs set up the main branch publishing workflow ([#9221](https://github.com/openshift/hypershift/pull/9221)), added manual trigger support for docs deployment ([#9261](https://github.com/openshift/hypershift/pull/9261)), and fixed the Wrangler-based deploy step ([#9252](https://github.com/openshift/hypershift/pull/9252)). The migration gives HyperShift's docs faster builds, reproducible environments via lockfile, and a modern publishing pipeline.

**Force destroy with finalizer stripping.** [@bryan-cox](https://github.com/bryan-cox)'s [PR #9134](https://github.com/openshift/hypershift/pull/9134) added a `--force` flag to `hypershift destroy cluster` that strips finalizers from HostedCluster resources when the grace period expires. Before this, a stuck finalizer — caused by a deleted cloud credential, a broken webhook, or an unreachable API server — could leave a HostedCluster in permanent deletion limbo. The force flag waits for the grace period, then removes all finalizers and deletes the resource. It's a big hammer, but sometimes you need one.

**Production bug fixes** were plentiful. [@andrej1991](https://github.com/andrej1991)'s [PR #9120](https://github.com/openshift/hypershift/pull/9120) bracketed IPv6 addresses in OAuth issuer and callback URLs — without brackets, the colon-separated IPv6 address gets parsed as a hostname:port pair, breaking authentication on dual-stack clusters. [@amasolov](https://github.com/amasolov)'s [PR #8581](https://github.com/openshift/hypershift/pull/8581) preserved KubeVirt userdata Secrets during NodePool rollouts — the controller was deleting the old Secret before the new nodes finished bootstrapping, causing in-flight nodes to lose their ignition config. [@csrwng](https://github.com/csrwng)'s [PR #9287](https://github.com/openshift/hypershift/pull/9287) scoped the NodePool config hash to only include `TLSSecurityProfile` from the APIServer config instead of the full object, preventing spurious rollouts when unrelated APIServer fields changed. [@reedcort](https://github.com/reedcort)'s [PR #9186](https://github.com/openshift/hypershift/pull/9186) handled `Unknown` status in `ClusterVersionFailing` condition inversion — a three-valued logic bug where `Unknown` was treated as `False`. [@matlaj](https://github.com/matlaj)'s [PR #9296](https://github.com/openshift/hypershift/pull/9296) fixed the kas-connection-checker to run as non-root, resolving a pod security violation. [@hlipsig](https://github.com/hlipsig)'s [PR #9031](https://github.com/openshift/hypershift/pull/9031) fixed Konnectivity agent authentication to use the cluster CA instead of the service CA, resolving TLS handshake failures in environments where the two CAs differ.

**NodePool MHC RemediationAllowed propagation.** [@sdminonne](https://github.com/sdminonne)'s [PR #9019](https://github.com/openshift/hypershift/pull/9019) added 452 lines to surface MachineHealthCheck `RemediationAllowed` status on the NodePool `Ready` condition. When MHC determines that remediating a machine would violate the `maxUnhealthy` threshold, that information was previously invisible to NodePool consumers. Now the NodePool condition reflects why unhealthy nodes aren't being remediated, giving operators actionable information instead of a silent "unhealthy but nothing is happening."

**HCCO webhook validation extraction.** [@bryan-cox](https://github.com/bryan-cox)'s [PR #9239](https://github.com/openshift/hypershift/pull/9239) moved 342 lines of webhook validation logic from inline in the HCCO admission handler into a dedicated `webhookvalidation` controller with 425 lines of focused tests. The motivation was separation of concerns — the admission handler was doing both mutation and validation, making it difficult to test validation logic in isolation and impossible to run validation checks outside the webhook context (e.g., in reconcile loops that need the same invariant checks).

---

## :material-chart-bar: By the Numbers

| Metric | Value |
|--------|-------|
| Total PRs merged | 279 |
| Unique contributors | 65 |
| Bot PRs | 54 |
| hypershift PRs | 202 |
| ai-helpers PRs | 5 |
| enhancements PRs | 1 |
| release PRs | 71 |
| Average merge time | 240.6 hours |
| High-impact PRs | 0 |
| Breaking changes | 0 |
| API changes | 0 |
| Customer-reported fixes | 10 |

**Top Reviewers**

| Reviewer | PRs Reviewed |
|----------|-------------|
| [@bryan-cox](https://github.com/bryan-cox) | 82 |
| [@jparrill](https://github.com/jparrill) | 20 |
| [@csrwng](https://github.com/csrwng) | 17 |
| [@ironcladlou](https://github.com/ironcladlou) | 15 |
| [@muraee](https://github.com/muraee) | 15 |

---

## :octicons-people-24: Contributors

Click any column header to sort. Each number links to the contributor's PRs in that repository.

| Contributor | hypershift | ai-helpers | enhancements | release | :material-bug: bugs | Total |
|------------|:-: | :-: | :-: | :-:|:-:|:-:|
| [@bryan-cox](https://github.com/bryan-cox) | [24](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-07-23..2026-08-22) | [1](https://github.com/openshift-eng/ai-helpers/pull/648) |  | [6](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-07-23..2026-08-22+hypershift) | [8](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **31** |
| [@ironcladlou](https://github.com/ironcladlou) | [10](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-07-23..2026-08-22) |  |  | [5](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-07-23..2026-08-22+hypershift) | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **15** |
| [@celebdor](https://github.com/celebdor) | [12](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **12** |
| [@clebs](https://github.com/clebs) | [11](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aclebs+merged%3A2026-07-23..2026-08-22) |  |  |  | [9](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aclebs+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **11** |
| [@vsolanki12](https://github.com/vsolanki12) | [9](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-07-23..2026-08-22) | [1](https://github.com/openshift-eng/ai-helpers/pull/607) |  |  | [8](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **10** |
| [@csrwng](https://github.com/csrwng) | [7](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acsrwng+merged%3A2026-07-23..2026-08-22) |  | [1](https://github.com/openshift/enhancements/pull/2042) |  | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Acsrwng+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **8** |
| [@dhgautam99](https://github.com/dhgautam99) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adhgautam99+merged%3A2026-07-23..2026-08-22) |  |  |  | [7](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Adhgautam99+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **8** |
| [@jparrill](https://github.com/jparrill) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-07-23..2026-08-22) |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-07-23..2026-08-22+hypershift) | [1](https://github.com/openshift/hypershift/pull/9014) | **8** |
| [@muraee](https://github.com/muraee) | [7](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amuraee+merged%3A2026-07-23..2026-08-22) |  |  |  | [4](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Amuraee+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **7** |
| [@amogh-redhat](https://github.com/amogh-redhat) | [1](https://github.com/openshift/hypershift/pull/8282) |  |  | [5](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aamogh-redhat+merged%3A2026-07-23..2026-08-22+hypershift) | [1](https://github.com/openshift/hypershift/pull/8282) | **6** |
| [@hlipsig](https://github.com/hlipsig) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ahlipsig+merged%3A2026-07-23..2026-08-22) |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ahlipsig+merged%3A2026-07-23..2026-08-22+hypershift) | [1](https://github.com/openshift/hypershift/pull/9031) | **6** |
| [@Nirshal](https://github.com/Nirshal) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3ANirshal+merged%3A2026-07-23..2026-08-22) |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3ANirshal+merged%3A2026-07-23..2026-08-22+hypershift) |  | **5** |
| [@hector-vido](https://github.com/hector-vido) |  |  |  | [5](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ahector-vido+merged%3A2026-07-23..2026-08-22+hypershift) |  | **5** |
| [@mgencur](https://github.com/mgencur) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-07-23..2026-08-22) |  |  | [1](https://github.com/openshift/release/pull/82859) |  | **5** |
| [@devguyio](https://github.com/devguyio) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adevguyio+merged%3A2026-07-23..2026-08-22) |  |  | [1](https://github.com/openshift/release/pull/82749) | [1](https://github.com/openshift/hypershift/pull/8891) | **4** |
| [@enxebre](https://github.com/enxebre) |  | [3](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-07-23..2026-08-22) |  | [1](https://github.com/openshift/release/pull/82469) |  | **4** |
| [@mehabhalodiya](https://github.com/mehabhalodiya) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amehabhalodiya+merged%3A2026-07-23..2026-08-22) |  |  |  |  | **4** |
| [@rutvik23](https://github.com/rutvik23) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Arutvik23+merged%3A2026-07-23..2026-08-22) |  |  | [1](https://github.com/openshift/release/pull/82073) | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Arutvik23+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **4** |
| [@apahim](https://github.com/apahim) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aapahim+merged%3A2026-07-23..2026-08-22) |  |  | [1](https://github.com/openshift/release/pull/83388) |  | **3** |
| [@bennerv](https://github.com/bennerv) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Abennerv+merged%3A2026-07-23..2026-08-22) |  |  |  | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Abennerv+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **3** |
| [@gangwgr](https://github.com/gangwgr) | [1](https://github.com/openshift/hypershift/pull/8953) |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Agangwgr+merged%3A2026-07-23..2026-08-22+hypershift) |  | **3** |
| [@machine424](https://github.com/machine424) |  |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amachine424+merged%3A2026-07-23..2026-08-22+hypershift) |  | **3** |
| [@maxcao13](https://github.com/maxcao13) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amaxcao13+merged%3A2026-07-23..2026-08-22) |  |  |  |  | **3** |
| [@openshift-ci](https://github.com/openshift-ci) |  |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aopenshift-ci+merged%3A2026-07-23..2026-08-22+hypershift) |  | **3** |
| [@sdminonne](https://github.com/sdminonne) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Asdminonne+merged%3A2026-07-23..2026-08-22) |  |  |  |  | **3** |
| [@JoelSpeed](https://github.com/JoelSpeed) | [1](https://github.com/openshift/hypershift/pull/9231) |  |  | [1](https://github.com/openshift/release/pull/82800) | [1](https://github.com/openshift/hypershift/pull/9231) | **2** |
| [@PoornimaSingour](https://github.com/PoornimaSingour) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **2** |
| [@avollmer-redhat](https://github.com/avollmer-redhat) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aavollmer-redhat+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aavollmer-redhat+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **2** |
| [@ckandag](https://github.com/ckandag) | [1](https://github.com/openshift/hypershift/pull/9222) |  |  | [1](https://github.com/openshift/release/pull/82930) |  | **2** |
| [@deepsm007](https://github.com/deepsm007) |  |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Adeepsm007+merged%3A2026-07-23..2026-08-22+hypershift) |  | **2** |
| [@georgelipceanu](https://github.com/georgelipceanu) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ageorgelipceanu+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Ageorgelipceanu+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **2** |
| [@germanparente](https://github.com/germanparente) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Agermanparente+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Agermanparente+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **2** |
| [@ingvagabund](https://github.com/ingvagabund) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aingvagabund+merged%3A2026-07-23..2026-08-22) |  |  |  |  | **2** |
| [@jhjaggars](https://github.com/jhjaggars) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajhjaggars+merged%3A2026-07-23..2026-08-22) |  |  |  |  | **2** |
| [@matlaj](https://github.com/matlaj) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amatlaj+merged%3A2026-07-23..2026-08-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Amatlaj+merged%3A2026-07-23..2026-08-22+OCPBUGS&type=pullrequests) | **2** |
| [@ricardomaraschini](https://github.com/ricardomaraschini) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aricardomaraschini+merged%3A2026-07-23..2026-08-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9213) | **2** |
| [@Pacho20](https://github.com/Pacho20) |  |  |  | [1](https://github.com/openshift/release/pull/78812) |  | **1** |
| [@ShazaAldawamneh](https://github.com/ShazaAldawamneh) | [1](https://github.com/openshift/hypershift/pull/8952) |  |  |  |  | **1** |
| [@Tamas-Biro1](https://github.com/Tamas-Biro1) | [1](https://github.com/openshift/hypershift/pull/9127) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9127) | **1** |
| [@amasolov](https://github.com/amasolov) | [1](https://github.com/openshift/hypershift/pull/8581) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8581) | **1** |
| [@amisstea](https://github.com/amisstea) |  |  |  | [1](https://github.com/openshift/release/pull/82440) |  | **1** |
| [@andrej1991](https://github.com/andrej1991) | [1](https://github.com/openshift/hypershift/pull/9120) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9120) | **1** |
| [@bmeng](https://github.com/bmeng) |  |  |  | [1](https://github.com/openshift/release/pull/82458) |  | **1** |
| [@chdeshpa-hue](https://github.com/chdeshpa-hue) | [1](https://github.com/openshift/hypershift/pull/8622) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8622) | **1** |
| [@cwilkers](https://github.com/cwilkers) | [1](https://github.com/openshift/hypershift/pull/8305) |  |  |  |  | **1** |
| [@dfajmon](https://github.com/dfajmon) | [1](https://github.com/openshift/hypershift/pull/8954) |  |  |  |  | **1** |
| [@everettraven](https://github.com/everettraven) | [1](https://github.com/openshift/hypershift/pull/9208) |  |  |  |  | **1** |
| [@fishereskew](https://github.com/fishereskew) | [1](https://github.com/openshift/hypershift/pull/9060) |  |  |  |  | **1** |
| [@holysoles](https://github.com/holysoles) | [1](https://github.com/openshift/hypershift/pull/9185) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9185) | **1** |
| [@jcmoraisjr](https://github.com/jcmoraisjr) | [1](https://github.com/openshift/hypershift/pull/9040) |  |  |  |  | **1** |
| [@jsafrane](https://github.com/jsafrane) |  |  |  | [1](https://github.com/openshift/release/pull/83094) |  | **1** |
| [@judexzhu](https://github.com/judexzhu) | [1](https://github.com/openshift/hypershift/pull/8957) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8957) | **1** |
| [@kurwang](https://github.com/kurwang) |  |  |  | [1](https://github.com/openshift/release/pull/83201) |  | **1** |
| [@maximunited](https://github.com/maximunited) |  |  |  | [1](https://github.com/openshift/release/pull/80919) |  | **1** |
| [@michaelryanmcneill](https://github.com/michaelryanmcneill) | [1](https://github.com/openshift/hypershift/pull/9152) |  |  |  |  | **1** |
| [@not-stbenjam](https://github.com/not-stbenjam) |  |  |  | [1](https://github.com/openshift/release/pull/82801) |  | **1** |
| [@psalajova](https://github.com/psalajova) |  |  |  | [1](https://github.com/openshift/release/pull/83384) |  | **1** |
| [@reedcort](https://github.com/reedcort) | [1](https://github.com/openshift/hypershift/pull/9186) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9186) | **1** |
| [@roivaz](https://github.com/roivaz) |  |  |  | [1](https://github.com/openshift/release/pull/82980) |  | **1** |
| [@sdodson](https://github.com/sdodson) | [1](https://github.com/openshift/hypershift/pull/9184) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9184) | **1** |
| [@shaheen0b111](https://github.com/shaheen0b111) | [1](https://github.com/openshift/hypershift/pull/9192) |  |  |  |  | **1** |
| [@sonia-garudi](https://github.com/sonia-garudi) |  |  |  | [1](https://github.com/openshift/release/pull/83161) |  | **1** |
| [@stbenjam](https://github.com/stbenjam) | [1](https://github.com/openshift/hypershift/pull/9172) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9172) | **1** |
| [@twolff-gh](https://github.com/twolff-gh) | [1](https://github.com/openshift/hypershift/pull/8865) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8865) | **1** |
| [@vismishr](https://github.com/vismishr) | [1](https://github.com/openshift/hypershift/pull/8749) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8749) | **1** |

---

## :material-crystal-ball: What's Next

The work merged in this period sets up several major threads for the coming months.

**CAPI v1beta2 migration rollout.** The migration infrastructure is complete and gated behind a feature flag. The next step is controlled rollout across fleet management clusters, monitoring for edge cases in CRDs with unusual managed field histories, and eventually promoting the migration to run by default.

**Karpenter standalone validation.** The deployment path exists behind TechPreviewNoUpgrade. Scale testing, upgrade path validation, and operational runbook development need to happen before the standalone mode can become the default, enabling independent Karpenter releases.

**E2E v2 expansion.** AWS lifecycle tests are running. The next targets are Azure and KubeVirt platform coverage, expanding the test matrix to cover topology transitions, upgrade scenarios, and the new OSImageStream graduation. The architectural docs and linting rules are designed to make onboarding new test authors smoother.

**Linter coverage expansion.** Ten analyzers shipped this month. The plugin framework makes adding new analyzers straightforward — each is a self-contained `analysis.Analyzer` with its own test fixtures. Candidates for future analyzers include checking for correct error wrapping patterns, validating condition reason strings, and detecting unannotated feature-gated code paths.

**Azure Managed HSM at scale.** The API and CPO support are in place. Validation with real Managed HSM deployments and integration with the compliance test suite are the remaining gaps before the feature can be recommended for production use.

The pace continues.
