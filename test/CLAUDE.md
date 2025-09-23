# HyperShift Testing Framework

## Overview
This directory contains the comprehensive testing framework for HyperShift, including unit tests, integration tests, end-to-end tests, and testing utilities.

## Test Categories

### Unit Tests
Unit tests are distributed throughout the codebase alongside source code. Run all unit tests with:
```bash
make test
```

### Integration Tests (`integration/`)
Kind-based integration testing framework for local development and validation of features that don't require cloud-provider-specific functionality.

**Key Components:**
- `integration_test.go` - Main integration test suite
- `framework/` - Test framework utilities and helpers
- `run.sh` - Script for setting up and running integration tests
- `hosted_cluster_sizing_test.go` - HostedCluster sizing validation
- `control_plane_pki_operator_test.go` - Control plane PKI tests
- `placeholder_test.go` - Placeholder and template tests

**Usage:**
```bash
# Setup kind cluster and run tests
./test/integration/run.sh cluster-up image setup test

# Run subset of tests
GO_TEST_FLAGS='-run TestSpecific' ./test/integration/run.sh test

# Refresh code changes
./test/integration/run.sh image reload
```

### End-to-End Tests (`e2e/`)
Full end-to-end testing across multiple cloud platforms. See `e2e/CLAUDE.md` for detailed documentation.

**Key Features:**
- AWS, Azure, OpenStack platform testing
- Cluster lifecycle management
- NodePool operations and upgrades
- Chaos engineering tests
- Autoscaling validation

**Build and Run:**
```bash
make e2e                    # Build e2e test binary
bin/test-e2e               # Run e2e tests (requires proper setup)
```

### Test Utilities (`util/`)
Shared testing utilities used across the testing framework:
- `pki.go` - PKI testing utilities
- `pki_test.go` - PKI utility tests

### Test Setup (`setup/`)
Test environment setup and configuration utilities:
- `main.go` - Main setup program
- `cluster-monitoring-config.yaml` - Monitoring configuration
- `fake_credentials` - Test credentials

### Hack Scripts (`hack/`)
Additional testing utilities and scripts:
- `nodeport-ip-aws/` - AWS NodePort IP utilities

## Make Targets

### Core Testing Commands
```bash
make test          # Run unit tests with race detection
make tests         # Compile all tests
make e2e           # Build e2e test binary
make vet           # Run go vet on all packages
```

### Specialized Testing
```bash
make regenerate-pki        # Regenerate PKI components and run related tests
make karpenter-upstream-e2e # Run Karpenter upstream e2e tests
```

## Test Configuration

### Environment Variables
- `NUM_CORES` - Number of parallel test jobs (default: detected)
- `PULL_SECRET` - Required for integration tests
- `WORK_DIR` - Working directory for integration tests

### Build Tags
- `integration` - Integration tests
- `e2e` - End-to-end tests
- `tools` - Tool building

## Development Workflow

### Running Tests Locally
1. **Unit Tests:** `make test` - Fast feedback for code changes
2. **Integration Tests:** Use `./test/integration/run.sh` for local validation
3. **E2E Tests:** Requires cloud credentials and infrastructure

### Test Development Best Practices
- Follow existing test patterns and utilities
- Use appropriate build tags
- Include proper cleanup logic
- Add tests for new features
- Validate both success and failure scenarios

### Debugging Tests
- Use `-v` flag for verbose output
- Set appropriate log levels in test frameworks
- Leverage the integration framework's artifact collection
- Check test-specific environment requirements

## Dependencies
- **Kind** - For integration testing
- **Container runtime** - Docker, Podman, or Buildah
- **Cloud credentials** - For e2e testing
- **Pull secret** - For OCP release access

## Contributing
When adding new tests:
1. Choose the appropriate test category (unit/integration/e2e)
2. Follow existing patterns and frameworks
3. Include proper documentation
4. Ensure tests are deterministic and reliable
5. Add cleanup logic for any resources created
6. Update this documentation for new test categories or significant changes