# HyperShift E2E v2 Testing Framework

## Overview

The HyperShift E2E v2 testing framework is a Ginkgo v2-based test harness designed for comprehensive end-to-end testing of HyperShift clusters. This framework provides improved structure, better reporting, shared utilities, and enhanced CI integration compared to the original e2e testing approach.

## Features

- **Ginkgo v2 Framework**: Modern BDD-style testing with improved reporting and parallel execution
- **JUnit XML Reports**: Comprehensive test reporting for CI/CD integration
- **Shared Test Infrastructure**: Reusable components for cluster management and platform-specific setup
- **Multi-Platform Support**: AWS, Azure, KubeVirt, OpenStack, and PowerVS platforms
- **Artifact Management**: Automatic collection and publishing of test artifacts
- **Configurable Test Execution**: Flexible test configuration via command-line flags

## Quick Start

### Building the Test Binary

```bash
# Build the e2ev2 test binary and Ginkgo CLI
make e2ev2
```

### Running Tests Locally

```bash
# Run all tests
./bin/ginkgo -v ./bin/test-e2ev2

# Run tests with specific configuration
./bin/ginkgo -v ./bin/test-e2ev2 -- \
  -e2e.platform=aws \
  -e2e.artifact-dir=/tmp/artifacts \
  -e2e.aws-credentials-file=/path/to/aws/credentials \
  -e2e.aws-region=us-east-1 \
  -e2e.pull-secret-file=/path/to/pull-secret \
  -e2e.base-domain=example.com

# Run tests with JUnit output
./bin/ginkgo -v --junit-report=junit.xml ./bin/test-e2ev2
```

## Framework Architecture

### Directory Structure

```
test/e2ev2/
├── README.md                    # This file
├── e2ev2_suite_test.go         # Main Ginkgo test suite
├── framework/                   # Shared test framework
│   ├── framework.go            # Core framework functionality
│   ├── options.go              # Test configuration options
│   ├── helpers.go              # Test utilities and helpers
│   ├── platform_aws.go         # AWS-specific setup
│   ├── platform_azure.go       # Azure-specific setup
│   └── platform_kubevirt.go    # KubeVirt-specific setup
├── examples/                    # Example test implementations
│   ├── basic_cluster_test.go   # Basic cluster lifecycle tests
│   ├── nodepool_test.go        # NodePool management tests
│   └── upgrade_test.go         # Upgrade scenario tests
└── assets/                     # Test assets and manifests
    └── test-manifests/         # YAML manifests for tests
```

### Core Components

#### Test Framework (`framework/`)

The framework package provides the foundation for all e2e tests:

- **Framework**: Central coordinator for test execution
- **TestOptions**: Configuration management and validation
- **ClusterManager**: Cluster lifecycle management
- **Platform Adapters**: Platform-specific resource management

#### Test Suite (`e2ev2_suite_test.go`)

The main Ginkgo test suite that:
- Initializes the test framework
- Configures reporters (console and JUnit)
- Manages global setup and teardown
- Handles signal interruption gracefully

## Configuration

### Command Line Flags

#### Platform-Agnostic Options

| Flag | Description | Default |
|------|-------------|---------|
| `-e2e.platform` | Target platform (aws, azure, kubevirt, etc.) | `aws` |
| `-e2e.artifact-dir` | Directory for test artifacts | `""` |
| `-e2e.base-domain` | Base domain for cluster ingress | `""` |
| `-e2e.pull-secret-file` | Path to pull secret file | `""` |
| `-e2e.ssh-key-file` | Path to SSH public key file | `""` |
| `-e2e.latest-release-image` | Latest OCP release image | `""` |
| `-e2e.node-pool-replicas` | Number of replicas per node pool | `2` |
| `-e2e.cluster-creation-timeout` | Timeout for cluster creation | `30m` |
| `-e2e.skip-teardown` | Skip cluster cleanup after tests | `false` |

#### AWS-Specific Options

| Flag | Description | Default |
|------|-------------|---------|
| `-e2e.aws-credentials-file` | Path to AWS credentials file | `""` |
| `-e2e.aws-region` | AWS region | `us-east-1` |
| `-e2e.aws-oidc-s3-bucket-name` | S3 bucket for OIDC provider | `""` |

#### Azure-Specific Options

| Flag | Description | Default |
|------|-------------|---------|
| `-e2e.azure-credentials-file` | Path to Azure credentials file | `""` |
| `-e2e.azure-location` | Azure location | `eastus` |

#### KubeVirt-Specific Options

| Flag | Description | Default |
|------|-------------|---------|
| `-e2e.kubevirt-infra-kubeconfig` | Path to infra cluster kubeconfig | `""` |
| `-e2e.kubevirt-infra-namespace` | Infra cluster namespace | `""` |

## Writing Tests

### Basic Test Structure

```go
package e2ev2

import (
    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
    "github.com/openshift/hypershift/test/e2ev2/framework"
)

var _ = Describe("Cluster Lifecycle", func() {
    var (
        cluster *framework.TestCluster
    )

    BeforeEach(func() {
        By("Creating a test cluster")
        cm := framework.NewClusterManager(testFramework)
        var err error
        cluster, err = cm.CreateTestCluster(ctx, framework.TestClusterOptions{
            Platform: hyperv1.AWSPlatform,
            NodePoolReplicas: 2,
        })
        Expect(err).NotTo(HaveOccurred())
    })

    AfterEach(func() {
        if cluster != nil && cluster.CleanupFunc != nil {
            By("Cleaning up test cluster")
            err := cluster.CleanupFunc(ctx)
            Expect(err).NotTo(HaveOccurred())
        }
    })

    It("should create and become ready", func() {
        By("Waiting for cluster to become ready")
        framework.WaitForClusterAvailable(testFramework, cluster)

        By("Waiting for node pools to become ready")
        framework.WaitForNodePoolsAvailable(testFramework, cluster)
    })
})
```

### Helper Functions

The framework provides several helper functions for common operations:

```go
// Create a cluster with automatic cleanup
cluster := framework.CreateClusterWithCleanup(framework, opts)

// Wait for cluster availability
framework.WaitForClusterAvailable(framework, cluster)

// Wait for node pools to be ready
framework.WaitForNodePoolsAvailable(framework, cluster)
```

### Test Labels and Organization

Use Ginkgo labels to organize tests:

```go
var _ = Describe("AWS Platform Tests", Label("aws", "platform"), func() {
    // AWS-specific tests
})

var _ = Describe("Upgrade Tests", Label("upgrade", "slow"), func() {
    // Upgrade-related tests
})
```

## CI Integration

### GitHub Actions Workflow

The framework includes a comprehensive GitHub Actions workflow (`.github/workflows/e2ev2.yml`) that:

1. **Builds** the test binary and validates compilation
2. **Runs** tests against multiple platforms (AWS, Azure)
3. **Collects** test artifacts including JUnit XML reports
4. **Publishes** test results with visual reports
5. **Comments** on pull requests with test summaries

### Artifact Collection

Test artifacts are automatically collected when the `-e2e.artifact-dir` flag is specified:

- **JUnit XML Reports**: `junit-e2ev2-{platform}.xml`
- **Test Logs**: Platform-specific log files
- **Cluster Resources**: YAML dumps of cluster resources
- **Debug Information**: Framework debug logs

### Running in CI

The workflow can be triggered by:
- Pushes to the main branch
- Pull requests with changes to relevant paths
- Manual trigger with the `run-e2e` label

## Best Practices

### Test Organization

1. **Group related tests** using Ginkgo's `Describe` blocks
2. **Use meaningful labels** for test categorization
3. **Implement proper cleanup** in `AfterEach` hooks
4. **Follow the AAA pattern**: Arrange, Act, Assert

### Resource Management

1. **Always implement cleanup functions** for created resources
2. **Use timeouts** for long-running operations
3. **Handle interruption gracefully** with context cancellation
4. **Reuse shared resources** when possible

### Error Handling

1. **Use Gomega matchers** for clear assertions
2. **Provide descriptive failure messages** with `By()` steps
3. **Log relevant context** when tests fail
4. **Clean up resources** even when tests fail

## Development Workflow

### Adding New Tests

1. Create test files in the appropriate subdirectory
2. Follow the established naming convention (`*_test.go`)
3. Use the shared framework for cluster management
4. Add appropriate labels for test categorization
5. Include cleanup logic for any created resources

### Testing Framework Changes

1. Run unit tests for the framework: `go test ./test/e2ev2/framework/...`
2. Build the test binary: `make e2ev2`
3. Run a subset of tests locally to validate changes
4. Update documentation if adding new features

### Debugging

1. Use `-v` flag for verbose output
2. Set `GINKGO_TRACE=1` for detailed trace information
3. Use `--focus` to run specific tests during debugging
4. Check artifact directory for detailed logs and dumps

## Contributing

When contributing to the e2e v2 framework:

1. **Follow existing patterns** for consistency
2. **Add tests** for new framework functionality
3. **Update documentation** for new features or changes
4. **Ensure CI passes** before submitting pull requests
5. **Consider backwards compatibility** when making changes

## Troubleshooting

### Common Issues

1. **Test binary build failures**: Check Go version and dependencies
2. **Missing credentials**: Verify platform-specific credential files
3. **Timeout errors**: Adjust timeout values for slow environments
4. **Cleanup failures**: Check RBAC permissions and resource dependencies

### Getting Help

- Review the [existing test examples](examples/)
- Check the [framework documentation](framework/)
- Consult the [original e2e tests](../e2e/) for reference patterns
- Open an issue for framework bugs or feature requests