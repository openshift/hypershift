# Migrating from V1 to V2

## Procedure for Porting a Test for One Platform

A test's meaning depends on its setup, guards, shared assertions, and execution
history. Programmatic analysis can expose that structure, but deciding whether a
different test preserves its guarantees requires a specification the code itself
does not provide. This procedure makes that judgment explicit and auditable
through small, reviewed transfers of responsibility, with progress measured by
concrete retirement of v1 execution.

Use this procedure after a person or tool identifies a v1 test or code region to
port for a particular platform. The unit of migration is a bounded transfer of
test responsibility: introduce any missing v2 replacement and retire the
corresponding v1 execution for that platform in the same commit. Existing v2
coverage can serve as the replacement when the review establishes that it is
sufficient.

The procedure produces a source change and a review record. It does not require
new test identities or a semantic coverage database. Acceptance records a judgment
that the replacement is sufficient for the stated scope; it does not prove
general equivalence between the suites.

### 1. Define the Source and Platform Scope

Identify the v1 registration or code region and the platform whose execution will
be retired. Describe the behavior it currently exercises using concrete source
references. Identify the relevant platform configurations and execution contexts
in which the replacement must run. If v2 does not yet provide those contexts,
establish them or narrow the proposed retirement.

Keep the transfer small enough that a reviewer can understand everything removed
and where its responsibilities go. One v1 test may require several v2 specs, and
several v1 checks may already be provided by one v2 scenario.

### 2. Trace the Effective V1 Test

Inspect the selected code together with its setup, manifest builders, shared
helpers, before/after hooks, guards, and cleanup. Establish which assertions and
scenario responsibilities would disappear if the selected v1 execution stopped.

Describe what the implementation actually verifies. Distinguish fatal assertions,
nonfatal test errors, and log-only observations. Account for skips and early
returns: checks before a skipped operation may still execute, and a skip inside a
nested subtest may still allow the caller's later checks.

### 3. Choose an Independently Transferable Boundary

State what moves to v2 and what remains in v1. Retain shared code needed by other
callers or platforms. If skipping an enclosing test would also remove unrelated
checks, provide their replacements or narrow the skip boundary. Do not claim the
transfer is complete while responsibilities removed by that boundary remain
unaccounted for.

Physical deletion of a shared helper is not required when other callers still
need it. Retirement concerns the selected platform's execution, rather than
removing every supporting source file.

### 4. Establish the V2 Replacement

Implement missing behavior or identify existing v2 behavior that provides the
responsibility. Preserve the relevant prerequisites, stimulus, assertions,
failure handling, and lifecycle context. For example, readiness after terminating
a worker has a different purpose from initial readiness even if both use the same
predicate.

Place responsibilities in the appropriate v2 test or lifecycle component and
follow the [v2 test conventions](writing-tests.md). Explain meaningful differences
in the replacement. Record suspected v1 defects separately rather than silently
strengthening or weakening the contract during translation. Intentional changes
or omissions require an explicit rationale in the review record.

### 5. Validate the Replacement

Run the relevant v2 behavioral tests in the agreed platform contexts. Inspect
selection, skips, early exits, and results to establish whether the intended
behavior was exercised. Distinguish an assertion failure reported by an informing
spec from a genuine precondition skip or a successful assertion; process exit
status alone is insufficient.

Where practical, introduce a controlled failure to demonstrate that the
replacement detects a relevant defect. Record what each experiment establishes.
A successful run against a healthy system alone does not demonstrate equivalent
defect detection.

Record the commands, results, and any unavailable validation. Disclose uncertainty
so reviewers can assess it rather than treating an unexecuted or skipped path as
validated.

### 6. Retire the Corresponding V1 Execution Atomically

In the same commit as any required replacement, delete the transferred code or
exclude its execution for the selected platform. Inspect the complete change to
confirm that other platforms and unmigrated scenarios retain their required
assertions, setup, and cleanup. Avoid unrelated changes that obscure the transfer.

Follow the v2 conventions for informing tests. Explicitly record any change in
failure gating when retiring v1 execution; porting a test does not itself promote
a v2 spec to blocking.

### 7. Review and Accept the Transfer

The reviewer examines both the replacement and everything the retirement removes.
For each responsibility removed from v1, the change must identify its v2 location
or an intentional omission with a rationale. Review the supporting evidence,
remaining uncertainty, and effects on execution and failure reporting.

Use this review record in the PR description:

```markdown
### Source and platform
V1 source references, selected platform, execution contexts, and retirement boundary.

### Replacement and preservation argument
V2 source references and how prerequisites, actions, assertions, and lifecycle
responsibilities are preserved.

### Differences and uncertainty
Intentional changes or omissions, effects on failure gating, responsibilities
remaining in v1, and unresolved concerns. State explicitly when there are none.

### Validation
Commands, execution contexts, results, skips, behavioral experiments, and checks
that were unavailable or not run.
```

### Commit Traceability

Use a consistent subject so migration commits are searchable:

```text
test(e2e): port <behavior> from v1 to v2 on <platform>
```

Include the following fields in the commit body, together with the explanation of
why and how the change transfers responsibility:

```text
V1-Source: <repository-relative path> — <function or subcase>
V2-Replacement: <repository-relative path> — <spec or lifecycle responsibility>
Platform: <platform>
Retirement: <what is deleted or excluded, and what remains>
```

Repeat source or replacement fields when the transfer spans multiple locations.
Use paths and symbols or scenario names; line numbers are optional because they
drift. The v1 references identify source in the commit's parent revision, and the
v2 references identify the replacement in the resulting revision.

Keep the detailed preservation argument and validation evidence in the PR
description. Follow repository commit-message conventions, including the
`Signed-off-by` footer, and put the Jira tracking issue in the PR title rather
than the commit message.

Preserve the traceability fields and keep the replacement and retirement together
in the final commit after squashing or restructuring. When the replacement
already exists, the retirement commit records where that responsibility resides.

### Completion and Progress

A transfer is complete when its replacement and platform-specific v1 retirement
are committed together and accepted with the review record above. Identification,
partial implementation, or a replacement left running alongside the selected v1
execution does not complete the transfer.

Accepted commits provide the historical record. Track remaining v1 execution by
platform and record intentional removals separately from ports. These observations
describe migration work remaining; they do not imply a percentage of semantic
equivalence. No permanent inventory of semantic identities is required.

## The Fundamental Shift

The v1 and v2 test frameworks differ fundamentally in how they manage cluster lifecycle, not just in syntax or test organization.

In v1, each test owns its cluster lifecycle. `e2eutil.NewHypershiftTest(...).Execute(...)` creates a fresh cluster, runs the test function, then tears down the cluster. Tests are self-contained units that manage their own infrastructure:

```go
func TestMyFeature(t *testing.T) {
    ctx, cancel := context.WithCancel(testContext)
    defer cancel()

    clusterOpts := globalOpts.DefaultClusterOptions(t)

    e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
        // Test logic here - cluster is created, live, and will be destroyed after
    }).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "my-feature", globalOpts.ServiceAccountSigningKey)
}
```

In v2, clusters are pre-created infrastructure shared across tests. The `create-guests` binary provisions clusters before any tests run, and tests consume them as read-only resources via `TestContext`. Tests do not create or destroy clusters:

```go
var _ = Describe("My Feature", Label("my-feature"), func() {
    var testCtx *internal.TestContext

    BeforeEach(func() {
        testCtx = internal.GetTestContext()
        Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
        testCtx.ValidateHostedCluster()
    })

    It("should work correctly", func() {
        // Test logic here - cluster already exists, test is read-only
    })
})
```

!!! note "Simplified example"
    This example omits the `RegisterXxxTests` + `TestContextGetter` pattern used by real tests. See the [canonical test pattern](writing-tests.md#canonical-test-pattern) for the full structure.

This architectural change has several implications for migration:

- **Remove all cluster creation/teardown logic** - `e2eutil.NewHypershiftTest`, `Execute()`, and `globalOpts` are v1-only constructs.
- **Tests that customized cluster creation need new infrastructure** - If your v1 test created clusters with special NodePool configurations, specific release images, or non-default platform settings, you need to define a new `ClusterSpec` variant in the platform's `PlatformConfig` (see [Adding a New ClusterSpec](ci-pipeline.md#adding-a-new-clusterspec)).
- **Test setup shifts from imperative to declarative** - Instead of building a test object with options, you acquire a `TestContext` pointing to pre-existing infrastructure.

## V1 vs V2 at a Glance

| Aspect | V1 | V2 |
|--------|----|----|
| Framework | `testing.T` + Gomega | Ginkgo v2 + Gomega |
| Test structure | `func TestFoo(t *testing.T)` + `e2eutil.NewHypershiftTest` | `Describe`/`It` blocks + `TestContext` |
| Subtests | `t.Run()` | Nested `It` blocks |
| Test selection | Separate binaries / build tags | Ginkgo labels + `--label-filter` |
| Cluster lifecycle | Per-test creation via `e2eutil.NewHypershiftTest(...).Execute(...)` | Pre-created by `create-guests`, shared via `TestContext` |
| CI scripts | Inline bash in release repo | Compiled Go binaries in hypershift repo |
| Reporting | Ad-hoc (single failure → 4-5 reported failures) | Structured JUnit, one entry per `It` (Sippy/CR compatible) |
| Build tag | `e2e` | `e2ev2` (or `e2ev2 && backuprestore`) |

## Migrating Shared Helpers (Prerequisite)

Before porting a test, check if it calls shared helper functions in `test/e2e/util/`. V2 code must not reference `test/e2e/util` or its subpackages: the `e2eutilallowlist` analyzer denies new references, and only a per-file, per-scope baseline in `hack/tools/hypershiftlinter/analyzers/e2eutilallowlist/allowlist.go` covers references that existed when the rule was introduced.

Recommended workflow:

1. Identify all shared helpers your test calls (e.g., `util.EnsureNoCrashingPods`, `util.EnsureNodeCountMatchesNodePoolReplicas`)
2. Move the behavior into `test/e2e/v2/util`, using v2 packages and Ginkgo-native assertions
3. Port the test against the v2 helper

Do not widen a legacy helper's signature just so v2 code can call it. Remove the corresponding entry from `allowlist.go` as each caller migrates.

!!! warning "Helpers that call `t.Run()` need deeper refactoring"
    Ginkgo's `GinkgoTB()` does not implement `Run()`, so a helper that calls `t.Run()` internally cannot be reused from v2. Restructure helpers moved into `test/e2e/v2/util` to drop internal `t.Run()` calls -- for example, by having the helper return results that the caller asserts in separate `It` blocks, or by splitting the helper into smaller functions that do not need subtests.

## Porting Step-by-Step

Follow this checklist when migrating a test from v1 to v2:

1. **Remove cluster lifecycle management** - Delete `e2eutil.NewHypershiftTest`, `Execute()`, and `globalOpts` calls. Your test no longer creates or destroys clusters.

2. **Replace test function with `Describe` block** - Change from:
    ```go
    func TestFoo(t *testing.T) { ... }
    ```
    to:
    ```go
    var _ = Describe("Feature", Label("my-label"), func() { ... })
    ```

3. **Add `BeforeEach` setup** - Acquire and validate `TestContext`:
    ```go
    var testCtx *internal.TestContext

    BeforeEach(func() {
        testCtx = internal.GetTestContext()
        Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
        testCtx.ValidateHostedCluster()
    })
    ```

4. **Replace `t.Run()` subtests with `It` blocks** - Nested `t.Run()` calls become separate `It` specs:
    ```go
    // V1
    t.Run("subtest A", func(t *testing.T) { ... })
    t.Run("subtest B", func(t *testing.T) { ... })

    // V2
    It("should pass subtest A", func() { ... })
    It("should pass subtest B", func() { ... })
    ```

5. **Replace `t.Fatal`/`t.Errorf` with Gomega assertions** - If your v1 test still uses raw `testing.T` assertions (not Gomega), convert to `Expect().To()` and `Eventually()`:
    ```go
    // V1
    if err != nil {
        t.Fatalf("operation failed: %v", err)
    }

    // V2
    Expect(err).NotTo(HaveOccurred(), "operation failed")
    ```

6. **Add appropriate labels** - Every `Describe` and `It` block should have labels for filtering. See [writing tests: labels](writing-tests.md#labels-two-layer-model) for the label taxonomy.

7. **Change build tag** - Update from:
    ```go
    //go:build e2e
    ```
    to:
    ```go
    //go:build e2ev2
    ```
    (or `//go:build e2ev2 && backuprestore` for backup-restore tests)

8. **Add `Register*Tests()` export function** - Follow the [canonical test pattern](writing-tests.md#canonical-test-pattern) to make your test suite discoverable:
    ```go
    func RegisterMyFeatureTests(getTestCtx internal.TestContextGetter) {
        It("should do something", func() {
            tc := getTestCtx()
            // test specs using tc
        })
    }
    ```

!!! tip "Real-World Examples"
    PR [openshift/hypershift#8527](https://github.com/openshift/hypershift/pull/8527) demonstrates all these steps in practice, porting the lifecycle tests from v1 to v2.

## Common Pitfalls

### `testing.TB` lacks `Run()`

The `testing.TB` interface does not include the `Run()` method, so `t.Run()` subtests cannot be called from Ginkgo tests. You must restructure subtest logic as separate `It` blocks:

```go
// WRONG - GinkgoTB() does not support Run()
t := GinkgoTB()
t.Run("subtest", func(t *testing.T) { ... })

// RIGHT - use separate It blocks
It("should pass subtest", func() { ... })
```

### `BeforeEach` runs per `It`

`BeforeEach` hooks execute before every `It` spec. If you need one-time setup that should not repeat, use `BeforeAll` inside an `Ordered` container:

```go
var _ = Describe("Feature", Ordered, func() {
    var expensiveResource *SomeResource

    BeforeAll(func() {
        expensiveResource = createExpensiveResource()
    })

    It("test A", func() { /* uses expensiveResource */ })
    It("test B", func() { /* uses expensiveResource */ })
})
```

### `Eventually` needs explicit timeouts

Always pass `.WithTimeout()` and `.WithPolling()` to `Eventually` assertions. Without them, Gomega uses short defaults that may not suit cluster operations:

```go
// WRONG - relies on default 1s timeout
Eventually(func() error { return checkCondition() }).Should(Succeed())

// RIGHT - explicit timeouts for cluster ops
Eventually(func() error { return checkCondition() }).
    WithTimeout(5*time.Minute).
    WithPolling(10*time.Second).
    Should(Succeed())
```

### Build tag

Do not forget to change the build tag. Tests with the wrong tag will not be compiled by the v2 test binaries:

```go
// WRONG - v1 tag
//go:build e2e

// RIGHT - v2 tag
//go:build e2ev2
```

For backup-restore tests, use the combined tag:

```go
//go:build e2ev2 && backuprestore
```

## What Stays in V1

V1 tests still exist and are actively running in CI. Not all tests have been migrated to v2, and some may remain in v1 indefinitely.

Tests remain in v1 when:

- **They depend heavily on `t.Run()` subtest trees** - Complex nested subtest logic that does not map cleanly to Ginkgo `It` blocks.
- **They call helper functions that still require `*testing.T`** - Helpers that have not yet been widened to accept `testing.TB`.
- **They require unique cluster configurations** - Tests that need custom NodePool settings, specific platform configurations, or particular release images for which no `ClusterSpec` variant has been defined yet in the platform's `PlatformConfig`.

See `test/e2e/` for current v1 test locations. There is no exhaustive backlog of tests to migrate - tests are ported to v2 as platforms transition to the v2 framework and as test maintainers see value in the migration.
