# Writing V2 Tests

This guide teaches developers how to add new v2 tests to the HyperShift test suite.

## Test file conventions

Every v2 test file must follow these conventions:

- Start with `//go:build e2ev2` build tag
- Live in package `tests` under `test/e2e/v2/tests/`
- Be named `feature_area_test.go` (e.g., `hosted_cluster_health_test.go`)
- Export a `RegisterXxxTests(getTestCtx internal.TestContextGetter)` function

!!! note "Backup-restore tests"
    Tests that perform backup and restore operations use a combined build tag `//go:build e2ev2 && backuprestore`. These tests compile into a separate binary `bin/test-backuprestore` (via `make backuprestore-e2e`), not `bin/test-e2e-v2`. This separation allows backup-restore tests to run with different concurrency settings and lifecycle requirements.

## Suite bootstrap

The test suite bootstraps through `suite_test.go` in the `test/e2e/v2/tests/` package:

```go
//go:build e2ev2

package tests

import (
    "context"
    "testing"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    "github.com/openshift/hypershift/test/e2e/v2/internal"
    ctrl "sigs.k8s.io/controller-runtime"
    zap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestE2EV2(t *testing.T) {
    if internal.GetEnvVarValue("E2E_SHOW_ENV_HELP") != "" {
        internal.PrintEnvVarHelp()
        return
    }
    RegisterFailHandler(internal.InformingAwareFailHandler)
    RunSpecs(t, "HyperShift End To End Test Suite")
}

var _ = BeforeSuite(func() {
    ctx := context.Background()
    ctrl.SetLogger(zap.New())
    testCtx, err := internal.SetupTestContextFromEnv(ctx)
    Expect(err).NotTo(HaveOccurred(), "failed to setup test context")
    Expect(testCtx).NotTo(BeNil(), "test context should not be nil")
    internal.SetTestContext(testCtx)
})
```

Key points:

- `RegisterFailHandler(internal.InformingAwareFailHandler)` installs the custom handler that converts `Informing`-labeled test failures to skips
- `BeforeSuite` reads `E2E_HOSTED_CLUSTER_NAME` and `E2E_HOSTED_CLUSTER_NAMESPACE` from environment and creates a shared `TestContext`

## Canonical test pattern

Tests follow this standard pattern, based on `DeploymentGenerationTest` in `control_plane_workloads_test.go`:

```go
func DeploymentGenerationTest(getTestCtx internal.TestContextGetter) {
    Context("Deployment generation", func() {
        BeforeEach(func() {
            testCtx := getTestCtx()
            hostedCluster := testCtx.GetHostedCluster()
            if hostedCluster == nil || hostedCluster.CreationTimestamp.IsZero() ||
                time.Since(hostedCluster.CreationTimestamp.Time) > 4*time.Hour {
                Skip("Deployment generation test is only for recently created hosted clusters")
            }
        })

        for _, workload := range workloads {
            if workload.Type != "Deployment" { continue }
            Context(workload.Name, func() {
                It("should not indicate rapid rollouts", func() {
                    testCtx := getTestCtx()
                    hostedCluster := testCtx.GetHostedCluster()
                    if internal.ShouldSkipWorkloadForPlatform(workload, hostedCluster) {
                        Skip(fmt.Sprintf("workload %s is platform-specific and doesn't match cluster platform", workload.Name))
                    }
                    // ... get deployment, check generation ...
                    Expect(deployment.Generation).To(BeNumerically("<=", maxAllowedGeneration),
                        "Deployment %s has generation %d which exceeds max allowed %d",
                        workload.Name, deployment.Generation, maxAllowedGeneration)
                })
            })
        }
    })
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneWorkloads] Control Plane Workloads", Label("control-plane-workloads"), func() {
    var testCtx *internal.TestContext
    BeforeEach(func() {
        testCtx = internal.GetTestContext()
        Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
        testCtx.ValidateHostedCluster()
    })
    RegisterControlPlaneWorkloadsTests(func() *internal.TestContext { return testCtx })
})
```

The pattern:

1. `Register*Tests` functions take a `TestContextGetter` parameter
2. The top-level `Describe` block uses `Label(...)` for test filtering
3. The `Describe` block calls the register function to add test cases
4. `BeforeEach` in the top-level block validates the hosted cluster exists

## Labels (two-layer model)

The v2 framework uses a two-layer labeling model for test organization and filtering.

### Layer 1: Labels on test blocks

Labels are attached to `Describe` or `Context` blocks to categorize tests:

| Category | Labels |
|----------|--------|
| Lifecycle | `lifecycle`, `control-plane-upgrade`, `nodepool-lifecycle`, `nodepool-autoscaling`, `etcd-chaos`, `backup-restore` |
| Health/Compliance | `hosted-cluster-health`, `hosted-cluster-compliance`, `hosted-cluster-security`, `hosted-cluster-dns`, `hosted-cluster-metrics`, `hosted-cluster-image-registry`, `hosted-cluster-ccm`, `control-plane-workloads`, `routes` |
| Platform-specific | `Azure`, `GCP`, `hosted-cluster-azure`, `self-managed-azure-public`, `self-managed-azure-private`, `self-managed-azure-oauth-lb` |
| Meta | `Informing` |

### Layer 2: Label-filter expressions

The CI pipeline uses label-filter expressions in TestMatrix configurations to select which tests run for each cluster configuration. Example from Azure TestMatrix:

```go
Parallel: []TestGroup{
    {
        Name:        "public",
        ClusterFile: "cluster-name-public",
        LabelFilter: "self-managed-azure-public || nodepool-lifecycle",
        JUnitFile:   "junit_self_managed_azure_public.xml",
    },
    // ...
},
Sequential: []SequentialGroup{
    {
        Name: "upgrade",
        Steps: []TestGroup{
            {
                Name:        "control-plane-upgrade",
                ClusterFile: "cluster-name-upgrade",
                LabelFilter: "control-plane-upgrade",
                JUnitFile:   "junit_control_plane_upgrade.xml",
            },
            // additional steps run in order within this group
        },
    },
},
```

`Parallel` groups all run concurrently. Each `SequentialGroup` also runs concurrently with everything else, but its internal `Steps` run one after another -- if any step fails, subsequent steps are skipped.

!!! tip "Adding a test with an existing label"
    If your test uses a label already in a filter expression (e.g., `hosted-cluster-health`), it runs automatically in the appropriate CI jobs. If you introduce a new label, you must add it to existing filter expressions in the TestMatrix configuration in the hypershift repository (not the release repository).

## Sippy/CR test name annotations

All test name strings must include annotations for [Sippy Component Readiness](https://sippy.dptools.openshift.org) (CR) mapping. These are parsed from the full Ginkgo test path and enable automatic Jira component assignment and per-feature regression tracking.

**Required annotations:**

1. **`[sig-hypershift][Jira:Hypershift]`** — on every top-level `Describe` block. Maps all tests to the Hypershift Jira component.
2. **`[Feature:XYZ]`** — maps to a specific feature/capability. Placement depends on the file:
    - **On the `Describe`** when the entire file tests one cohesive feature (most files).
    - **On individual `Context`/`When` blocks** when a file covers multiple distinct capabilities.

Feature names use PascalCase with no spaces (e.g., `BackupRestore`, `AzurePrivateLink`, `NodePoolLifecycle`).

**Single-feature file:**

```go
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:Health] Hosted Cluster Health", Label("hosted-cluster-health"), func() {
    // all tests in this file map to Feature:Health
})
```

**Multi-feature file** (e.g., platform-specific files covering multiple capabilities):

```go
var _ = Describe("[sig-hypershift][Jira:Hypershift] Hosted Cluster Azure", Label("hosted-cluster-azure"), func() {
    // Jira component set at Describe level, Feature set per Context
})

Context("[Feature:AzureWorkloadIdentity] Azure Public Cluster", Label("Azure"), func() {
    It("should mutate pods with workload identity federated credentials", func() { ... })
})

Context("[Feature:AzurePrivateLink] Azure Private Topology", Label("Azure"), func() {
    It("should create AzurePrivateLinkService CR with PLS alias", func() { ... })
})
```

!!! warning "Do not rename tests after Sippy import"
    Renaming tests after Sippy imports them loses historical data and requires manual re-mapping. Add annotations before tests are imported.

Check existing Feature names before adding new ones:

```bash
grep -r '\[Feature:' test/e2e/v2/tests/
```

## Platform guards

Use `Skip` in `BeforeEach` to skip tests when platform preconditions are not met:

```go
BeforeEach(func() {
    testCtx := getTestCtx()
    hostedCluster := testCtx.GetHostedCluster()
    if hostedCluster == nil || hostedCluster.Spec.Platform.Type != hyperv1.AzurePlatform {
        Skip("Azure-specific test; skipping on non-Azure cluster")
    }
})
```

### Informing skip-conversion

The `Informing` label marks tests as informational. When an `Informing`-labeled test fails, the custom failure handler converts it to a skip instead of a failure:

```go
func InformingAwareFailHandler(message string, callerSkip ...int) {
    labels := CurrentSpecReport().Labels()
    if slices.Contains(labels, "Informing") {
        Skip("informing test failure: " + message, callerSkip...)
    }
    Fail(message, callerSkip...)
}
```

This allows tests to run and report failures without blocking CI jobs.

## TestContext

The `TestContext` struct provides access to the hosted cluster and clients:

| Field/Method | Description |
|-------------|-------------|
| `MgmtClient` | Management cluster controller-runtime client |
| `GetHostedCluster()` | Returns `*HostedCluster`, cached via `sync.Once`. Returns nil if not configured. **Panics** on fetch failure. |
| `GetHostedClusterClient()` | Returns hosted cluster controller-runtime client. Lazy-loaded. Returns nil if the HostedCluster is not available or its KubeConfig status is not set. **Panics** on other initialization failures. |
| `ClusterName` / `ClusterNamespace` | HostedCluster name and namespace from environment |
| `ControlPlaneNamespace` | Derived: `{namespace}-{name}` (e.g., `clusters-my-cluster`). Dots in the name are replaced with hyphens. |
| `ArtifactDir` | Directory for test artifacts, from the `ARTIFACT_DIR` environment variable |
| `ValidateHostedCluster()` | Skips if no cluster configured; panics if fetch fails |
| `ValidateHostedClusterClient()` | Calls `ValidateHostedCluster()`, then panics if the hosted cluster client is nil (e.g., kubeconfig not ready) |
| `HostedClusterConfigured` | `bool` — `true` when both `ClusterName` and `ClusterNamespace` are populated. This is what `ValidateHostedCluster()` checks internally to decide between skip and fetch. |
| `GetHostedClusterRESTConfig()` | Returns `*rest.Config` for the hosted cluster, lazy-loaded via `sync.Once`. Useful when a test needs a `kubernetes.Interface` client or a custom REST client beyond what `GetHostedClusterClient()` provides. |
| `Context` | Embedded `context.Context` — use for all API calls |

!!! warning "Panic-on-failure"
    `GetHostedCluster()`, `GetHostedClusterClient()`, and `ValidateHostedCluster()` panic on API failures (not just missing configuration). This ensures test failures are loud and visible rather than silently skipping important checks. Use `ValidateHostedCluster()` in top-level `BeforeEach` blocks to fail fast when the cluster is missing.

## Environment variables

The v2 framework maintains a registry of environment variables. To add a new variable:

```go
// In env_vars.go init()
RegisterEnvVar("E2E_MY_NEW_VAR", "Description of what it does", false)

// In test code
value := internal.GetEnvVarValue("E2E_MY_NEW_VAR")  // panics if unregistered
```

To see the full current list of registered environment variables:

```bash
E2E_SHOW_ENV_HELP=1 bin/test-e2e-v2
```

## Assertions and gotchas

### Direct Gomega assertions

```go
Expect(x).To(Equal(y))
Expect(ptr).NotTo(BeNil())
```

### Eventually for async checks

```go
Eventually(func() bool {
    // ... check condition ...
    return condition
}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(BeTrue())
```

For pointer safety, vacuous pass prevention, IPv6 URL construction, and the non-lifecycle vs. lifecycle mutation rule, see [AGENTS.md conventions #11, #13, #15, #16](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/AGENTS.md).

## Lifecycle vs non-lifecycle tests

### Non-lifecycle tests

Non-lifecycle tests are read-only and skip when preconditions are missing. They must not modify cluster state to create preconditions — see [AGENTS.md Convention #13](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/AGENTS.md) for the full rule and examples.

### Lifecycle tests

Lifecycle tests may modify cluster state but must:

- Use the `lifecycle` label
- Capture and restore original state in cleanup
- Check `IsNotFound()` in cleanup to handle missing resources gracefully

```go
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePoolLifecycle] NodePool Lifecycle", Label("lifecycle", "nodepool-lifecycle"), func() {
    var originalReplicas int32
    BeforeEach(func() {
        // Capture original state
        nodePool := getNodePool()
        Expect(nodePool.Spec.Replicas).NotTo(BeNil(),
            "nodePool %s/%s should have replicas set", nodePool.Namespace, nodePool.Name)
        originalReplicas = *nodePool.Spec.Replicas
    })
    AfterEach(func() {
        // Restore original state
        nodePool := getNodePool()
        nodePool.Spec.Replicas = &originalReplicas
        if err := mgmtClient.Update(ctx, nodePool); err != nil && !apierrors.IsNotFound(err) {
            Fail(fmt.Sprintf("failed to restore nodepool replicas: %v", err))
        }
    })
    // ... test cases ...
})
```

### Backup-restore tests

Backup-restore tests are a special case:

- Use combined build tag `//go:build e2ev2 && backuprestore`
- Use `Ordered, Serial` decorators to ensure sequential execution
- Compile into separate binary `bin/test-backuprestore` (via `make backuprestore-e2e`)
- Run with reduced parallelism to avoid resource contention

## Adding a workload to the registry

To add a new control plane workload to the compliance tests, add a `WorkloadSpec` entry to the slice returned by `GetControlPlaneWorkloads()` in `test/e2e/v2/internal/workload_registry.go`:

```go
{Name: "my-new-component", Type: "Deployment", PodSelector: map[string]string{"app": "my-new-component"}},
```

All existing compliance tests (pod security, resource requests/limits, image verification, etc.) automatically cover the new workload.

For platform-specific workloads, use the `Platform` field:

```go
azurePlatform := hyperv1.AzurePlatform
// ...
{Name: "azure-cloud-controller-manager", Type: "Deployment", PodSelector: map[string]string{"app": "cloud-controller-manager"}, Platform: &azurePlatform},
```

## Running tests locally

Set the required environment variables and run the test binary:

```bash
export KUBECONFIG=/path/to/management-cluster-kubeconfig
export E2E_HOSTED_CLUSTER_NAME=my-cluster
export E2E_HOSTED_CLUSTER_NAMESPACE=clusters

make e2ev2
bin/test-e2e-v2 --ginkgo.label-filter="hosted-cluster-health" --ginkgo.v

# For backup-restore tests
make backuprestore-e2e
bin/test-backuprestore --ginkgo.label-filter="backup-restore" --ginkgo.v

# See all registered environment variables
E2E_SHOW_ENV_HELP=1 bin/test-e2e-v2
```

Use `--ginkgo.label-filter` to run specific test categories. Use `--ginkgo.v` for verbose output. Use `--ginkgo.focus` to run a single test by name:

```bash
# Run a single test by name (regex match against the full description path)
bin/test-e2e-v2 --ginkgo.focus="should not indicate rapid rollouts" --ginkgo.v
```
