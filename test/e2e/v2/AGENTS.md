# v2 E2E Test Framework

## Overview

This is a Ginkgo v2 BDD test suite for validating hosted cluster control planes. Tests run against a pre-existing hosted cluster and verify workload compliance, API validation, and backup/restore lifecycle.

- Build tag: `//go:build e2ev2`
- Build: `make e2ev2` produces `bin/test-e2e-v2`
- Backup-restore tests use combined tags `e2ev2,backuprestore` via `make backuprestore-e2e`
- Suite entry point: `tests/suite_test.go` with `BeforeSuite` that initializes `TestContext` from environment variables

## Architecture

The framework is organized into three packages under `test/e2e/v2/`:

- `internal/` — Framework internals (test context, workload registry, fail handler, env var management). Do not add tests here.
- `tests/` — All standard v2 test files. Each file is feature-scoped with a top-level `Describe` and `Label`. The suite entry point is `suite_test.go`.
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

var _ = Describe("Control Plane Workloads", Label("control-plane-workloads"), func() {
    // BeforeEach gets TestContext, then calls RegisterControlPlaneWorkloadsTests
})
```

### 3. Fail-Loud Philosophy

Framework functions panic with diagnostic messages rather than returning errors silently. `GetHostedCluster()` uses `sync.Once` to fetch lazily and panics on failure. `GetEnvVarValue()` panics on unregistered variables. Tests assume the hosted cluster is fully operational before they run — there is no startup polling.

### 4. Test Assertion Patterns

Use direct Gomega assertions (`Expect(...).To(...)`) for stateless validation tests. Reserve `Eventually()` for lifecycle/mutation tests where state changes over time (e.g., backup-restore waiting for readiness). The vast majority of tests in this framework are stateless.

### 5. Platform Guards

Use `BeforeEach` with `Skip()` when a test applies only to specific platforms. Message format:

```go
if hostedCluster == nil || hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
    Skip("Pod affinities and tolerations test is only for AWS platform")
}
```

### 6. Context Lifecycle

Use `tc.Context` (the embedded `context.Context` in `TestContext`) for all API calls. Never use `context.Background()` except in test helpers where `TestContext` is not available.

### 7. Workload Registry

To add coverage for a new control plane workload, add a `WorkloadSpec` entry in `workload_registry.go`. This automatically includes it in all existing compliance tests (resource requests, pull policy, read-only filesystem, etc.). Platform-specific workloads use the `Platform` field for automatic filtering.

### 8. Environment Variable Registration

Register all environment variables via `RegisterEnvVar()` or `RegisterEnvVarWithDefault()` in `env_vars.go` before use. `GetEnvVarValue()` panics if the variable is not registered. This enforces a central catalog of all configuration.

### 9. State Mutation and Cleanup

When a test mutates cluster state, capture the original state before mutation and defer restoration. In cleanup functions, check `apierrors.IsNotFound()` to handle cases where the resource was already deleted.

### 10. Labels

Apply labels to `Describe` and `It` blocks for test filtering. Only apply labels to `Context` blocks when you have explicit filtering intent (e.g., `Label("Informing")` to mark non-blocking tests). The `Informing` label causes the custom fail handler to skip rather than fail.

### 11. Pointer Safety

Always nil-check pointers before dereferencing. Use diagnostic messages that include namespace/name:

```go
Expect(ptr).NotTo(BeNil(), "container %s in pod %s should have security context", container.Name, pod.Name)
```

### 12. Docstrings

Comments on exported functions must describe actual behavior including panic conditions, not just intended behavior. For example, `GetEnvVarValue` documents that it "panics if the environment variable is not registered."

## Expanding v2

When adding new test areas:

1. Follow the established patterns in this document — `RegisterXxxTests`, `TestContextGetter`, platform guards, etc.
2. If no existing pattern covers your use case, consult the `#wg-hypershift-e2e-v2` working group channel before establishing new conventions.
3. Flag any new patterns in PR reviews so the working group can discuss and ratify them.
4. Update this AGENTS.md once patterns are agreed upon by the working group.

## Gotchas

- `backuprestore/` tests use `Ordered, Serial` Ginkgo decorators and require the combined `e2ev2,backuprestore` build tag. They produce a separate binary (`bin/test-backuprestore`).
- `workload_registry.go` has a header comment saying "generated" but the file is manually maintained. Edit it directly.
- Tests assume the hosted cluster is fully operational. There is no startup polling or readiness waiting in the suite setup.
