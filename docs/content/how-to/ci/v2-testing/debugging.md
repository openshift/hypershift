# Debugging CI Failures

This guide explains how to diagnose failing v2 CI jobs by tracing test failures to their source clusters and reading diagnostic artifacts.

## Finding Test Results

Each `TestGroup` produces a JUnit XML file named by its `JUnitFile` field. These land in `ARTIFACT_DIR` in the Prow job artifacts.

For example, the Azure self-managed job produces:

- `junit_self_managed_azure_public.xml`
- `junit_self_managed_azure_private.xml`
- `junit_self_managed_azure_oauth_lb.xml`
- `junit_nodepool_autoscaling.xml`
- `junit_lifecycle_upgrade.xml`
- `junit_lifecycle_etcd_chaos.xml`

Additionally, `create-guests` emits `junit_hosted_cluster_{name}.xml` for each cluster that reaches Phase 4 (version rollout wait), recording either success or failure. On failure, the JUnit file contains the `HostedCluster` and `NodePool` conditions at the time of failure. On success, it records a passing test case confirming the rollout completed.

## Mapping Failures to Clusters

To find which cluster a failing test ran against, trace the path:

1. **JUnit file name** → `TestGroup.Name` (e.g., `junit_self_managed_azure_public.xml` → `"public"`)
2. **TestGroup.Name** → `TestGroup.ClusterFile` (e.g., `"public"` → `"cluster-name-public"`)
3. **ClusterFile** → cluster name derived from `PROW_JOB_ID` + variant (e.g., `public-a1b2c3d4e5`)

The `run-tests` step log shows the mapping explicitly:

```text
Running public tests against public-a1b2c3d4e5...
Running private tests against private-f6e7d8c9b0...
```

To find the cluster name for a failing test, search the `run-tests` step log for the line matching the test group name.

## Reading Ginkgo Verbose Output

The `--ginkgo.v` flag produces verbose output that maps test failures to their source code structure.

When a test is running, Ginkgo prints the full test description. The exact format depends on the Ginkgo version; with Ginkgo v2 it looks approximately like:

```text
Control Plane Workloads Deployment generation kube-apiserver should not indicate rapid rollouts
```

This maps to the nested test block structure:

```go
Describe("Control Plane Workloads") →
    Context("Deployment generation") →
        Context("kube-apiserver") →
            It("should not indicate rapid rollouts")
```

On failure, Ginkgo prints:

- The exact `It` block description
- The assertion that failed (e.g., `Expected <5> to be <= <3>`)
- File path and line number (e.g., `control_plane_workloads_test.go:42`)
- Full label set (e.g., `[control-plane-workloads]`)

Use this information to locate the failing test in the codebase and understand what condition was violated.

## create-guests Failures

The most common failure point in v2 jobs is Phase 4 (version rollout wait) in `create-guests`. When this happens:

1. **Check for JUnit XML**: Look for `junit_hosted_cluster_*.xml` in artifacts
2. **Read conditions**: The JUnit file contains `HostedCluster` and `NodePool` conditions at the time of failure
3. **No JUnit file?**: If no JUnit file exists, the failure happened before Phase 4 — check the `create-guests` step log for earlier phases

Common pre-Phase 4 failures:

- **Phase 1 (cluster creation)**: `hypershift create cluster` command failure — check for invalid flags or missing credentials
- **Phase 2 (post-create hooks)**: Platform-specific hook failure — check for API errors when patching resources
- **Phase 3 (wait Available)**: Timeout waiting for `HostedClusterAvailable` condition — indicates control plane startup failure
- **Phase 5 (write cluster names)**: Failure writing cluster names to `SHARED_DIR` — rare, typically caused by filesystem or permissions errors

## dump-guests Artifacts

`hypershift dump cluster` collects must-gather, events, pod logs, and other diagnostics. These appear in the Prow artifact directory under `artifacts/<step-name>/`.

The `dump-guests` binary exits non-zero if required environment variables (`PROW_JOB_ID`, `ARTIFACT_DIR`) are missing. Once environment setup succeeds, it **always exits 0** even if individual cluster dumps fail, so missing artifacts indicate a dump failure for that cluster. To diagnose:

1. Check the `dump-guests` step log for `WARNING` messages
2. Look for timeout or API errors when fetching resources
3. Verify the cluster still existed when dump ran (it may have been deleted by an earlier cleanup step)

Common artifacts to check:

- `namespaces/<control-plane-namespace>/pods/` — pod logs for control plane components
- `cluster-scoped-resources/events/` — cluster events showing resource creation/deletion
- `namespaces/<control-plane-namespace>/core/events.yaml` — control plane events
- `namespaces/openshift-*/` — hosted cluster namespace logs

## Understanding Informing Test Skips

When a test labeled `Informing` fails, the custom fail handler converts it to a skip. It appears as "skipped" (not "failed") in JUnit reports.

To see the actual failure reason, look at the Ginkgo verbose output in the `run-tests` step log. The skip message includes the original failure:

```text
[SKIP] informing test failure: Expected <false> to be true
```

The test failure does not block CI or appear in the JUnit summary. This is by design — `Informing` tests are informational only.

!!! warning "Informing tests are invisible to Sippy"
    Informing test failures do not appear in Sippy or Component Readiness dashboards. If you need a test to be tracked by these tools, do not use the `Informing` label.
