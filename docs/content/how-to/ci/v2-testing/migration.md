# Migrating from V1 to V2

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

## Refactoring Shared Helpers (Prerequisite)

Before porting a test, check if it calls shared helper functions in `test/e2e/util/`. Many helpers were originally written to accept `*testing.T`, not `testing.TB`. Since Ginkgo's `GinkgoTB()` returns `testing.TB`, these helpers cannot be called directly from v2 tests.

Recommended workflow:

1. Identify all shared helpers your test calls (e.g., `util.EnsureNoCrashingPods`, `util.EnsureNodeCountMatchesNodePoolReplicas`)
2. Check each helper's signature - does it take `*testing.T` or `testing.TB`?
3. If it takes `*testing.T`, refactor the helper to accept `testing.TB` in a separate PR
4. Then port the test

!!! info "Example: Lifecycle Test Helper Refactoring"
    PR [openshift/hypershift#8527](https://github.com/openshift/hypershift/pull/8527) widened 12 shared helpers from `*testing.T` to `testing.TB` as part of migrating the lifecycle tests. This pattern ensures helpers work with both v1 (`*testing.T`) and v2 (`GinkgoTB()`) tests during the transition period.

!!! warning "Helpers that call `t.Run()` need deeper refactoring"
    Widening `*testing.T` to `testing.TB` is not sufficient for helpers that call `t.Run()` internally. The `testing.TB` interface does not include the `Run()` method, so these helpers will fail to compile after the signature change. You must restructure such helpers to remove the internal `t.Run()` calls -- for example, by having the helper return results that the caller asserts in separate `It` blocks, or by splitting the helper into smaller functions that do not need subtests.

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
