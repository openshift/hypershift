# v2 E2E Test Framework

The invariants below govern all v2 framework changes, including migrated tests.
Existing code or examples that do not yet follow them are migration debt, not
exceptions or patterns to copy.

## Overview

This is a Ginkgo v2 BDD test suite for validating hosted cluster control planes. Tests run against a pre-existing hosted cluster and verify workload compliance, API validation, and backup/restore lifecycle.

- Build tag: `//go:build e2ev2`
- Build: `make e2ev2` produces `bin/test-e2e-v2`
- Backup-restore tests use combined tags `e2ev2,backuprestore` via `make backuprestore-e2e`
- Suite entry point: `tests/suite_test.go` with `BeforeSuite` that initializes `TestContext` from environment variables

## Architecture

The framework is organized into the following packages under `test/e2e/v2/`:

- `internal/` — Framework internals (test context, workload registry, fail handler, env var management). Do not add e2e tests here; standard Go unit tests (`func TestXxx(t *testing.T)`) for internal functions are fine.
- `tests/` — All standard v2 test files. Each file is feature-scoped with a top-level `Describe` and `Label`. The suite entry point is `suite_test.go`.
- `util/` — Shared test utilities (pod exec helpers, metrics fetching) consumed by test files. Unlike `internal/`, these are importable by other packages.
- `lifecycle/` — Platform configuration, cluster variant catalogs, test plans, test matrices, manifests, and lifecycle hooks.
- `cmd/` — CLI tools for test orchestration: creating/destroying/dumping guest clusters and running test suites.
- `backuprestore/` — Backup/restore helpers (CLI wrappers, prober, Velero).

Additional packages may be introduced as the v2 framework expands; this structure is not yet finalized.

## Established Standards

### 1. Terminology

Use "hosted cluster" and "control plane". Never use "guest cluster".

### 2. File Organization

Each test file is feature-scoped and exports a `RegisterXxxTests(getTestCtx TestContextGetter)` function that registers Ginkgo blocks. The top-level `Describe` uses a descriptive name and a `Label` for filtering. See `control_plane_workloads_test.go` for the canonical example:

```go
func RegisterControlPlaneWorkloadsTests(getTestCtx internal.TestContextGetter) {
    WorkloadRegistryValidationTest(getTestCtx)
    DeploymentGenerationTest(getTestCtx)
    // ...
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneWorkloads] Control Plane Workloads", Label("control-plane-workloads", internal.InformingLabel), func() {
    var tc *internal.TestContext
    BeforeEach(func() {
        tc = internal.GetTestContext()
        Expect(tc).NotTo(BeNil(), "test context should be set up in BeforeSuite")
    })
    // Register specs during tree construction, never inside BeforeEach/It.
    RegisterControlPlaneWorkloadsTests(func() *internal.TestContext { return tc })
})
```

### 3. Test Cluster Assumptions / Invariants

Tests consume pre-created hosted clusters. Provisioning, initial readiness, and
teardown belong to the lifecycle binaries, not individual specs or suite setup.
Do not add startup polling to tests to compensate for incomplete provisioning.
Lifecycle tests may wait for transitions they initiate; a state check may poll
when the behavior under test is asynchronous (see convention #4).

### 4. Test Assertion Patterns

Use direct Gomega assertions (`Expect(...).To(...)`) for stateless validation.
Use `Eventually` only when verifying an asynchronous transition or convergence,
with an explicit context, timeout, and polling interval. Do not use retries to
hide invalid preconditions or startup failures.

**Every helper that makes assertions must call `GinkgoHelper()` at entry and
accept a `g Gomega` argument** (`gomega.Gomega` with qualified imports). Make all
assertions through that instance: `g.Expect`, `g.Eventually`, and
`g.Consistently`. Pass it through to any assertion helpers called in turn. This
also applies to helpers whose main purpose is setup or cleanup but which assert
internally. Registration functions that only declare Ginkgo nodes are not
assertion helpers.

`GinkgoHelper()` attributes failures to the caller; passing `g` preserves the
caller's failure handler, including retry handling within `Eventually`. See
[Gomega's helper guidance](https://onsi.github.io/gomega/#making-assertions-in-helper-functions).

```go
func expectHostedCluster(g Gomega, tc *internal.TestContext) *hyperv1.HostedCluster {
    GinkgoHelper()
    hc, err := tc.GetHostedCluster()
    g.Expect(err).NotTo(HaveOccurred())
    g.Expect(hc).NotTo(BeNil())
    return hc
}

// A helper that polls must use g.Eventually, never the global Eventually.
func expectNodePoolReplicas(g Gomega, tc *internal.TestContext, key crclient.ObjectKey, want int32) {
    GinkgoHelper()
    g.Eventually(func(pollG Gomega) {
        np := &hyperv1.NodePool{}
        pollG.Expect(tc.MgmtClient.Get(tc.Context, key, np)).To(Succeed())
        pollG.Expect(np.Status.Replicas).To(Equal(want))
    }).WithContext(tc.Context).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed())
}
```

In a spec body, pass `Default` to call an assertion helper directly. Within an
`Eventually(func(g Gomega) { ... })` callback, pass that callback's `g` instead.
Inside a nested polling callback, use its own supplied instance (`pollG` above),
including for further helper calls. Global assertions inside such callbacks
bypass retry handling and can prematurely fail or skip an informing spec.

### 5. Platform Guards

Use `BeforeEach` with `Skip()` when a test applies only to specific platforms. Message format:

```go
if hostedCluster == nil || hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
    Skip("Pod affinities and tolerations test is only for AWS platform")
}
```

Fetch with `hostedCluster, err := tc.GetHostedCluster()` and assert the error
before checking platform or configuration preconditions. API failures must fail
the assertion, not be converted into a missing-precondition skip. The
`TestContext` getters return values and errors; they do not cache the
HostedCluster or provide `ValidateHostedCluster()` methods.

### 6. Context Lifecycle

Use `tc.Context` (the embedded `context.Context` in `TestContext`) for all API calls. Never use `context.Background()` except in test helpers where `TestContext` is not available.

### 7. Workload Registry

To add coverage for a new control plane workload, add a `WorkloadSpec` entry in `workload_registry.go`. This automatically includes it in all existing compliance tests (resource requests, pull policy, read-only filesystem, etc.). Platform-specific workloads use the `Platform` field for automatic filtering.

### 8. Environment Variable Registration

Register test-suite environment variables via `RegisterEnvVar()` or `RegisterEnvVarWithDefault()` in `internal/env_vars.go` before use. `GetEnvVarValue()` panics if the variable is not registered. Lifecycle binaries also have their own CLI/environment configuration; do not confuse that with the test-suite registry.

### 9. State Mutation and Cleanup

When a test mutates cluster state, capture the original state before mutation and defer restoration. In cleanup functions, check `apierrors.IsNotFound()` to handle cases where the resource was already deleted.

When creating a resource, register `DeferCleanup` immediately after `Create` — not later in the test flow. If the test fails before reaching a manual deletion, the resource leaks. Manual deletion is fine as part of test verification, but `DeferCleanup` ensures cleanup on all exit paths.

```go
Expect(hcClient.Create(tc.Context, secret)).To(Succeed())
DeferCleanup(func() {
    err := hcClient.Delete(tc.Context, secret)
    if err != nil && !apierrors.IsNotFound(err) {
        Expect(err).NotTo(HaveOccurred(), "cleanup: failed to delete %s", secret.Name)
    }
})
```

### 10. Labels

**All new tests, including tests migrated from v1, must be `Informing` by
default.** Add `Label(internal.InformingLabel)` to each new `It`, or inherit it from a
`Describe`/`Context` containing only informing tests. When adding a spec to an
existing blocking container, label the new spec or a new nested container rather
than changing the lifecycle of existing tests. Promotion to blocking is a
separate, deliberate change, not part of adding coverage.

Use the `internal.InformingLabel` constant rather than the string literal when
applying this label in code.

Apply feature/filter labels to `Describe` and `It` blocks. Only label `Context`
blocks with explicit filtering intent, including the informing lifecycle.
`Informing` does not exclude tests from execution: the custom fail handler
converts assertion failures into skips for process exit status, and the JUnit
renderer records the original informing failures (see convention #20).

```go
It("should satisfy the new invariant", Label(internal.InformingLabel), func() {
    // assertions
})
```

### 11. Pointer Safety

Always nil-check pointers before dereferencing. Use diagnostic messages that include namespace/name:

```go
Expect(ptr).NotTo(BeNil(), "container %s in pod %s should have security context", container.Name, pod.Name)
```

### 12. Docstrings

Comments on exported functions must describe actual behavior including panic conditions, not just intended behavior. For example, `GetEnvVarValue` documents that it "panics if the environment variable is not registered."

### 13. Non-Lifecycle Tests Must Not Mutate the Hosted Cluster

Non-lifecycle tests (health, compliance, security, metrics) must only **verify** existing state — never mutate the hosted cluster to create preconditions. If a test requires a specific annotation, label, or configuration to be present, `Skip()` when it is absent rather than setting it. Only lifecycle tests (upgrade, backup-restore, nodepool scaling) may mutate cluster state.

```go
// WRONG — sets annotation to create precondition
hc.Annotations["hypershift.openshift.io/metrics-forwarder"] = "true"
Expect(mgmtClient.Update(ctx, hc)).To(Succeed())

// RIGHT — skip if precondition is missing
if _, ok := hc.Annotations["hypershift.openshift.io/metrics-forwarder"]; !ok {
    Skip("metrics forwarder annotation not set on hosted cluster")
}
```

### 14. Per-Workload Test Placement

Tests that iterate over control plane workloads (e.g., checking restart counts, custom labels, custom tolerations) belong in `control_plane_workloads_test.go`, not in health or compliance test files. Follow the per-workload pattern: define a separate `It` block for each registered workload so failures identify the exact workload.

```go
for _, w := range workloads {
    workload := w
    Context(workload.Name, func() {
        It("should have custom labels", func() {
            // assert per-workload
        })
    })
}
```

### 15. IPv6-Safe URL Construction

When building URLs from endpoint IPs (e.g., Kubernetes service endpoints), always use `net.JoinHostPort` instead of `fmt.Sprintf`. Plain `%s:%d` formatting produces invalid URLs for IPv6 addresses.

```go
// WRONG — breaks with IPv6
kasAddress = fmt.Sprintf("https://%s:%v", ip, port)

// RIGHT — brackets IPv6 addresses automatically
kasAddress = "https://" + net.JoinHostPort(ip, fmt.Sprintf("%d", port))
```

### 16. Vacuous Pass Prevention

Before iterating a list and asserting on each item, assert the list is non-empty. An empty list trivially passes all per-item assertions, hiding regressions where resources were never created.

```go
Expect(routeList.Items).NotTo(BeEmpty(),
    "expected at least one route in namespace %s", tc.ControlPlaneNamespace)
for i := range routeList.Items {
    // per-item assertions
}
```

This also applies to condition-search loops: when iterating with `if match { ... break }`, add a `found` boolean and assert it's true after the loop. An unmatched loop silently passes.

```go
found := false
for _, cond := range nodePool.Status.Conditions {
    if cond.Type == expectedType {
        found = true
        // assertions on cond
        break
    }
}
Expect(found).To(BeTrue(), "expected condition %s on NodePool %s", expectedType, nodePool.Name)
```

### 17. Idempotent Resource Creation

When a helper function creates a resource that might already exist, don't silently return on `AlreadyExists`. Either reconcile the existing resource to the desired state (Get + Update) or fail with a diagnostic message. Silent return masks stale state that can invalidate test baselines.

```go
err := hcClient.Create(tc.Context, secret)
if apierrors.IsAlreadyExists(err) {
    existing := &corev1.Secret{}
    Expect(hcClient.Get(tc.Context, crclient.ObjectKeyFromObject(secret), existing)).To(Succeed())
    existing.Data = secret.Data
    existing.Type = secret.Type
    Expect(hcClient.Update(tc.Context, existing)).To(Succeed())
    return
}
Expect(err).NotTo(HaveOccurred(), "failed to create additional-pull-secret")
```

### 18. Dynamic Assertion Values

When a test extracts a value from one resource to validate against another (e.g., `expectedStrategy` from HostedCluster checked against IngressController), use the extracted variable in assertions — not hardcoded enum constants. Hardcoded values make the test brittle and hide the logical contract being verified.

```go
// WRONG — hardcoded constants
g.Expect(ic.Spec.EndpointPublishingStrategy.Type).To(Equal(operatorv1.LoadBalancerServiceStrategyType))

// RIGHT — compare against the extracted expectation
g.Expect(ic.Spec.EndpointPublishingStrategy.Type).To(Equal(expectedStrategy.Type))
```

### 19. Sippy/CR Test Name Annotations

All test name strings must include annotations for Sippy Component Readiness (CR) mapping. These annotations are parsed from the full Ginkgo test path and enable automatic Jira component assignment and per-feature regression tracking.

**Required annotations:**

1. **`[sig-hypershift][Jira:Hypershift]`** — on every top-level `Describe` block. Maps all tests to the Hypershift Jira component.

2. **`[Feature:XYZ]`** — maps to a specific feature/capability. Placement depends on the file:
   - **On the `Describe`** when the entire file tests one cohesive feature (most files).
   - **On individual `Context`/`When` blocks** when a file covers multiple distinct capabilities.

Feature names use PascalCase with no spaces (e.g., `BackupRestore`, `AzurePrivateLink`, `NodePoolLifecycle`).

**Single-feature file example:**

```go
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Health] Hosted Cluster Health", Label("hosted-cluster-health", internal.InformingLabel), func() {
    // all tests in this file map to Feature:Health
})
```

**Multi-feature file example:**

```go
var _ = Describe("[sig-hypershift][Jira:Hypershift] Hosted Cluster Azure", Label("hosted-cluster-azure", internal.InformingLabel), func() {
    // Jira component set at Describe level, Feature set per Context
})

// In the registration function:
Context("[Feature:AzureWorkloadIdentity] Azure Public Cluster", Label("Azure", "self-managed-azure-public"), func() {
    It("should mutate pods with workload identity federated credentials", func() { ... })
})

Context("[Feature:AzurePrivateLink] Azure Private Topology", Label("Azure", "self-managed-azure-private"), func() {
    It("should create AzurePrivateLinkService CR with PLS alias", func() { ... })
})
```

**When adding new test files:** Choose a Feature name that maps to a distinct capability. Check existing Feature names in the codebase (`grep -r '\[Feature:' test/e2e/v2/tests/`) to avoid duplicates.

### 20. JUnit Contract

The v2 suite owns its JUnit output through `internal.GenerateJUnitReport`; do not
enable Ginkgo's built-in `--ginkgo.junit-report`. Pass the destination with
`--e2e.junit-report` instead.

The downstream contract is:

- Ginkgo decorator labels are omitted from testcase names. Textual Sippy/CR
  annotations in `Describe`, `Context`, and `It` strings remain part of the name.
- Ginkgo leaf node types such as `[It]` are omitted from testcase names, and
  suite setup nodes are not emitted as testcases.
- Every `It` spec selected by the invocation's label filter produces exactly
  one testcase. Specs excluded by that filter are omitted rather than reported
  as skipped because separate `run-tests` invocations are implementation-level
  shards of the same downstream suite.
- The testsuite name remains `hypershift-e2e`; informing results must not be
  split into a differently named suite.
- Every spec labeled `Informing` has both a `lifecycle="informing"` testcase
  attribute and a `<property name="lifecycle" value="informing"/>` property.
- Assertion failures converted to skips by `InformingAwareFailHandler` are
  rendered as informing failures with their original message. Genuine calls to
  `Skip` remain skipped.
- Informing assertion failures handled by `InformingAwareFailHandler` do not
  cause the test process to exit unsuccessfully; regular failures do.

Run `make test-e2ev2-unit` to exercise the renderer and the synthetic Ginkgo
contract suite. The standard `make test` target also runs these tests.

### 21. No v1 E2E Package Dependencies

Do not use v1 package code in v2 tests.

### 22. Static Cluster Variant Contract

Each `PlatformConfig.ClusterSpecs` implementation must return a fixed
set of unique variant names defined at compile time. Never add, remove, or
rename variants at runtime: their names are a contract with external test plans.

### 23. Declarative Platform Cluster Specs

Keep `PlatformConfig.ClusterSpecs` as declarative as possible: a readable
`[]ClusterSpec` literal with explicit variant names, shared base arguments, and
small helpers for composing variant-specific arguments. Keep configuration
differences visible alongside their variants instead of hiding them in control
flow or generic builders.

Resolve runtime values separately where practical. Limit unavoidable setup I/O
to obtaining values needed by the specs (for example, resolving a KMS alias to
an ARN); it must not change the variant catalog. Put resource provisioning and
day-2 mutations in the appropriate `PreCreate`, `PostCreate`, `PostAvailable`,
or `PostVersionRollout` hooks. Test selection and execution ordering belong in
`TestPlan`/`TestMatrix`, not `ClusterSpecs`.

## Framework Changes Require Advance Discussion

Adding test cases should follow the existing framework and platform contracts.
**Before submitting changes beyond adding new test cases, discuss the proposal
with the `#forum-ocp-hypershift` and agree on the approach.** This
includes changes to platform interfaces, lifecycle orchestration, shared
framework helpers, test selection, and reporting.

Explain why the existing framework cannot meet the need, consider alternatives,
and assess the impact on other platforms, tests, and external test plans.
Reference the prior discussion in the PR. Do not introduce framework changes
incidentally while adding test coverage or defer the design discussion to PR
review.

## Expanding v2

When adding new test areas:

1. Follow the established patterns in this document — `RegisterXxxTests`, `TestContextGetter`, platform guards, etc.
2. If no existing pattern covers your use case, consult the `#wg-hypershift-e2e-v2` working group channel before establishing new conventions.
3. Reference the agreed approach in PR reviews for any new patterns.
4. Update this AGENTS.md once patterns are agreed upon by the working group.

### 21. Legacy E2E Utilities

V2 code must not reference `test/e2e/util` or its subpackages. The `e2eutilallowlist` analyzer enforces this against a per-file, per-scope baseline in `hack/tools/hypershiftlinter/analyzers/e2eutilallowlist/allowlist.go`; do not expand the baseline for new code. Implement the behavior in `test/e2e/v2/util` instead, and remove the corresponding entry when an existing caller migrates.

## Gotchas

- `backuprestore/` tests use `Ordered, Serial` Ginkgo decorators and require the combined `e2ev2,backuprestore` build tag. They produce a separate binary (`bin/test-backuprestore`).
- `internal/workload_registry.go` is manually maintained. Edit it directly.
- Tests assume the hosted cluster is fully operational. There is no startup polling or readiness waiting in the suite setup.
- MicroShift skip guards (`exutil.IsMicroShiftCluster()`, `[Skipped:MicroShift]` labels) do **not** apply to v2 e2e tests. Those guards are scoped to `openshift-tests-private`. The v2 framework runs exclusively against hosted clusters, which are never MicroShift.
