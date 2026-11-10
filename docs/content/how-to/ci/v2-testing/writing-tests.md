# Writing V2 Tests

Follow these invariants when adding or migrating v2 E2E tests. Existing tests
that violate them are migration debt, not examples to copy.

For hooks, decorators, and filtering, consult the
[Ginkgo documentation](https://onsi.github.io/ginkgo/).

## Choose the test boundary

Tests consume pre-created, fully operational hosted clusters. Provisioning,
initial readiness, and teardown belong to lifecycle orchestration. Do not add
startup polling to specs or suite setup to compensate for incomplete provisioning.

Non-lifecycle tests only inspect existing state. Skip when a required platform
or configuration precondition is absent; never mutate the cluster to manufacture
it. API failures must fail the test rather than become precondition skips.

Lifecycle tests may mutate state and wait for transitions they initiate. Label
them `lifecycle` so that read-only runs can exclude them. Capture original state
before mutation and register restoration. Register `DeferCleanup` immediately
after creating a resource, even when the scenario later deletes it explicitly.
Cleanup must tolerate resources already being absent while surfacing other errors.
If a resource already exists, reconcile it to the required state or fail with a
diagnostic; silently accepting it can invalidate the test baseline.

## Structure coverage

Put feature-scoped specs in `test/e2e/v2/tests/` and shared utilities in `util/`.
Use the registration pattern in
[`control_plane_workloads_test.go`](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/tests/control_plane_workloads_test.go):
register specs during Ginkgo tree construction and obtain the runtime test
context inside hooks or specs. Do not register specs inside `BeforeEach` or `It`.

The [suite](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/tests/suite_test.go)
supplies a shared
[`TestContext`](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/internal/test_context.go).
Fetch the HostedCluster and assert the returned error before testing platform or
configuration preconditions:

```go
BeforeEach(func() {
    tc := getTestCtx()
    hc, err := tc.GetHostedCluster()
    Expect(err).NotTo(HaveOccurred())
    if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
        Skip("test requires AWS")
    }
})
```

Re-fetch objects when checking mutations; do not assert against stale state.

Tests iterating over control plane workloads belong in
`control_plane_workloads_test.go`, with separate specs per workload so failures
identify the affected component. Add new workloads to the manually maintained
[`workload_registry.go`](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/internal/workload_registry.go)
to extend existing compliance coverage, using its platform filtering where needed.

## Assert the intended behavior

Assert current state directly. Poll only for asynchronous behavior or transitions
the test initiates. Choose timeouts and polling intervals appropriate to the
operation. Use `tc.Context` for API calls and polling so both use the context
provided by the framework.

Assertion helpers must preserve the caller's failure handling. A direct call
uses Gomega's `Default`; inside a polling callback, pass that callback's Gomega
instance through every helper. Nested callbacks use their own instance. Global
assertions inside polling callbacks bypass retry handling and may prematurely
fail or skip an informing spec. This applies to setup and cleanup helpers that
assert internally, too. See
[Gomega's helper guidance](https://onsi.github.io/gomega/#making-assertions-in-helper-functions).

For example, an assertion helper fetches current state and uses the supplied
Gomega instance:

```go
func expectReplicas(g Gomega, tc *internal.TestContext, key crclient.ObjectKey, want int32) {
    GinkgoHelper()
    np := &hyperv1.NodePool{}
    g.Expect(tc.MgmtClient.Get(tc.Context, key, np)).To(Succeed())
    g.Expect(np.Status.Replicas).To(Equal(want))
}
```

Inside a spec, choose a direct check or polling according to the behavior under
test:

```go
// Check state that should already hold.
expectReplicas(Default, tc, key, want)

// Wait for convergence after a lifecycle test changes replicas.
Eventually(func(g Gomega) {
    expectReplicas(g, tc, key, want)
}).WithContext(tc.Context).
    WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(Succeed())
```

Derive expected values from configuration when testing relationships between
resources. Assert the outcome the scenario promises: an updated desired release
alone does not establish that an upgrade completed. Identify affected resources
and failed expectations in diagnostics.

Establish that the intended subjects were actually checked. Empty selections,
unmatched searches, or early returns must not silently produce a passing test
when they bypass the behavior it claims to verify.

## Select tests and preserve their meaning

All new tests, including migrated tests, start informing. Apply
`internal.InformingLabel` to the new spec or inherit it from a container containing
only informing tests. When extending an existing blocking container, label the
new coverage without changing existing tests. Promotion to blocking is a separate,
deliberate change. The [migration procedure](migration.md) defines the exception
for transferring existing blocking responsibility atomically.

Feature labels select coverage; the informing label controls failure handling.
Use labels on `Describe` and `It`; label `Context` only with explicit selection
intent, including informing status. Choose feature identities that represent
distinct capabilities and check existing names before adding one. Preserve test
names after Sippy import because renaming loses history and requires remapping.

Informing assertion failures are reported without failing the process; genuine
precondition skips remain skipped. A successful exit therefore does not mean all
assertions passed. Use `--e2e.junit-report` for the suite's custom reporting.
See the [reporting contract](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/internal/junit.go)
for the downstream format.

## Supply prerequisites and CI selection

Reuse an existing cluster variant when its configuration and state are compatible
with the test. A mutating test does not automatically need its own cluster;
isolate it when its prerequisites or effects cannot be shared safely.

Define cluster variants explicitly, keeping configuration differences visible.
Variant names are a contract with external plans: the catalog must be fixed at
compile time and must not change with runtime conditions. Resolve runtime values
separately where practical. Put provisioning and day-2 mutations in lifecycle
hooks, and selection and ordering in `TestPlan`/`TestMatrix`. Consult
[`platform.go`](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/lifecycle/platform.go)
for interfaces and hook timing.

A test runs wherever a plan's filter selects its labels. Update filters in the
HyperShift repository when adding coverage that existing filters do not select.
For job infrastructure changes, see [CI Pipeline Configuration](ci-pipeline.md).

Groups targeting the same cluster must share one sequential lane; separate lanes
run concurrently. A failed step prevents later steps in its lane from running.
Ensure the plan selects the intended configurations and post-mutation checks.

Register test-suite environment variables in
[`env_vars.go`](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/internal/env_vars.go)
before reading them. Use built-in help for the current variables.

## Verify and debug

Run against an already provisioned hosted cluster:

```bash
export KUBECONFIG=/path/to/management-cluster-kubeconfig
export E2E_HOSTED_CLUSTER_NAME=my-cluster
export E2E_HOSTED_CLUSTER_NAMESPACE=clusters
make e2ev2
E2E_SHOW_ENV_HELP=1 bin/test-e2e-v2
bin/test-e2e-v2 --ginkgo.label-filter="hosted-cluster-health" \
    --ginkgo.v --e2e.junit-report=/tmp/junit.xml
```

Use `--ginkgo.focus` to narrow execution by description. Inspect actual selection,
skips, assertion results, and cleanup; a successful exit or skipped path is not
evidence that the intended behavior ran.

In CI, find the group's JUnit report in `ARTIFACT_DIR`. Use the plan and
`run-tests` log to identify its cluster. Check verbose Ginkgo output for the
assertion and source location, and distinguish informing failures from genuine
skips.

For provisioning failures, inspect the `create-guests` log and rollout JUnit
conditions. Use `dump-guests` logs to locate events and pod logs or diagnose
missing artifacts. Successful teardown does not establish that collection
succeeded.

## Discuss framework changes first

Before submitting changes beyond adding test cases, discuss the proposal with
`#forum-ocp-hypershift` and agree on the approach. This includes platform
interfaces, lifecycle orchestration, shared helpers, selection, and reporting.
Explain why existing facilities are insufficient, consider alternatives and
cross-platform effects, and reference the agreement in the PR.

If no established pattern fits a new test area, consult
`#wg-hypershift-e2e-v2` before establishing a convention and update this guide
after agreement.
