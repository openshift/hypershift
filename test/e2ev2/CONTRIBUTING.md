# Contributing to HyperShift E2E v2 Testing Framework

Thank you for your interest in contributing to the HyperShift E2E v2 testing framework! This document provides guidelines for contributing tests, framework improvements, and documentation.

## Getting Started

### Prerequisites

- Go 1.24.0 or later
- Access to a Kubernetes cluster with HyperShift installed
- Platform-specific credentials (AWS, Azure, etc.)
- Pull secret for OCP images

### Development Setup

1. **Clone the repository** and navigate to the e2ev2 directory:
   ```bash
   cd test/e2ev2
   ```

2. **Build the test framework**:
   ```bash
   make e2ev2
   ```

3. **Run framework unit tests**:
   ```bash
   go test -v ./framework/...
   ```

## Contributing Guidelines

### Code Organization

- **Framework code** goes in the `framework/` directory
- **Example tests** go in the `examples/` directory
- **Test assets** (YAML manifests, etc.) go in the `assets/` directory
- **Platform-specific code** should be in separate files (e.g., `platform_aws.go`)

### Writing Tests

#### Test Structure

Follow the established patterns for test organization:

```go
var _ = Describe("Feature Name", Label("category", "subcategory"), func() {
    var (
        ctx    context.Context
        cancel context.CancelFunc
        // other variables
    )

    BeforeEach(func() {
        ctx, cancel = context.WithTimeout(context.Background(), timeoutDuration)
        // setup code
    })

    AfterEach(func() {
        defer cancel()
        // cleanup code
    })

    Describe("Specific Scenario", func() {
        It("should do something specific", func() {
            By("doing step 1")
            // test code

            By("doing step 2")
            // more test code

            By("verifying the result")
            // assertions
        })
    })
})
```

#### Test Labels

Use consistent labels to categorize tests:

- **Platform labels**: `aws`, `azure`, `kubevirt`, `openstack`, `powervs`
- **Feature labels**: `cluster`, `nodepool`, `upgrade`, `networking`, `storage`
- **Performance labels**: `slow`, `fast`, `performance`
- **Stability labels**: `stable`, `experimental`, `flaky`

Example:
```go
var _ = Describe("AWS Cluster Creation", Label("aws", "cluster", "stable"), func() {
    // test code
})
```

#### Best Practices

1. **Use descriptive test names** that clearly indicate what is being tested
2. **Include proper cleanup** in `AfterEach` hooks
3. **Use `By()` statements** to document test steps
4. **Handle timeouts appropriately** with context cancellation
5. **Use framework helpers** instead of duplicating common functionality
6. **Add appropriate labels** for test categorization

### Framework Development

#### Adding New Features

1. **Framework features** should go in the `framework/` package
2. **Platform-specific code** should be in separate files
3. **Add unit tests** for new framework functionality
4. **Update documentation** for new features

#### Platform Support

To add support for a new platform:

1. Create a new file `framework/platform_<platform>.go`
2. Implement the platform-specific setup and cleanup functions
3. Add platform-specific options to `framework/options.go`
4. Update the validation logic
5. Add example tests for the new platform

Example:
```go
// framework/platform_newplatform.go
func (f *Framework) setupNewPlatformResources(ctx context.Context) error {
    // Platform-specific setup
    return nil
}

func (f *Framework) cleanupNewPlatformResources(ctx context.Context) error {
    // Platform-specific cleanup
    return nil
}
```

### Testing Your Changes

#### Local Testing

1. **Run framework unit tests**:
   ```bash
   go test -v ./framework/...
   ```

2. **Build the test binary**:
   ```bash
   make e2ev2
   ```

3. **Run a subset of tests**:
   ```bash
   ./run.sh --focus "Basic Cluster" --platform aws
   ```

4. **Test with dry run**:
   ```bash
   ./run.sh --dry-run --platform aws
   ```

#### CI Testing

- Tests run automatically on pull requests
- Use the `run-e2e` label to trigger full e2e tests
- Check the CI workflow status and artifacts

### Documentation

#### Code Documentation

- Add godoc comments for exported functions and types
- Include examples in documentation where appropriate
- Document configuration options and their defaults

#### User Documentation

- Update `README.md` for user-facing changes
- Add examples to the examples directory
- Update the run script help text if adding new options

### Pull Request Process

1. **Create a feature branch** from main
2. **Make your changes** following the guidelines above
3. **Add tests** for new functionality
4. **Update documentation** as needed
5. **Run local tests** to ensure nothing is broken
6. **Create a pull request** with a clear description

#### PR Description Template

```markdown
## Description
Brief description of the changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Framework improvement
- [ ] Documentation update
- [ ] Test addition

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing performed

## Checklist
- [ ] Code follows project conventions
- [ ] Documentation updated
- [ ] Tests added/updated
- [ ] Labels updated
```

### Review Process

1. **Automated checks** must pass (build, tests, linting)
2. **Peer review** by framework maintainers
3. **Testing verification** for significant changes
4. **Documentation review** for user-facing changes

## Common Patterns

### Error Handling

```go
It("should handle errors gracefully", func() {
    By("attempting an operation that might fail")
    result, err := someOperation()

    if testScenarioExpectsFailure {
        Expect(err).To(HaveOccurred())
        Expect(err).To(MatchError(ContainSubstring("expected error")))
    } else {
        Expect(err).NotTo(HaveOccurred())
        Expect(result).To(Equal(expectedResult))
    }
})
```

### Resource Management

```go
var resource *SomeResource

BeforeEach(func() {
    By("creating test resource")
    var err error
    resource, err = createResource(ctx)
    Expect(err).NotTo(HaveOccurred())
})

AfterEach(func() {
    if resource != nil {
        By("cleaning up test resource")
        err := resource.Cleanup(ctx)
        if err != nil {
            // Log but don't fail the test
            testFramework.GetLogger("cleanup").Error(err, "Failed to cleanup resource")
        }
    }
})
```

### Platform-Specific Tests

```go
Describe("Platform-Specific Feature", func() {
    BeforeEach(func() {
        if !testFramework.GetTestOptions().IsAWS() {
            Skip("Skipping AWS-specific test")
        }
    })

    It("should work on AWS", func() {
        // AWS-specific test code
    })
})
```

## Getting Help

- **Framework questions**: Open an issue with the `question` label
- **Bug reports**: Open an issue with the `bug` label
- **Feature requests**: Open an issue with the `enhancement` label
- **Contributing help**: Reach out to framework maintainers

## Code of Conduct

Please follow the project's code of conduct when contributing. Be respectful, inclusive, and constructive in all interactions.

Thank you for contributing to HyperShift E2E v2!