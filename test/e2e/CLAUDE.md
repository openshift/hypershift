# HyperShift E2E Testing Guide

## Overview
This directory contains end-to-end tests for HyperShift, validating cluster creation, management, and various platform-specific functionality.

## Test Structure
The e2e tests are organized into several categories:

### Core Tests
- `e2e_test.go` - Main test suite setup and framework
- `create_cluster_test.go` - Cluster creation and lifecycle tests
- `control_plane_upgrade_test.go` - Control plane upgrade scenarios
- `upgrade_hypershift_operator_test.go` - HyperShift operator upgrade tests

### Platform-Specific Tests
- `azure_scheduler_test.go` - Azure platform scheduling tests
- `karpenter_test.go` - Karpenter node provisioning tests
- `nodepool_osp_advanced_test.go` - OpenStack platform tests

### NodePool Tests
Multiple test files covering nodepool functionality:
- `nodepool_test.go` - Core nodepool operations
- `nodepool_upgrade_test.go` - NodePool upgrade scenarios
- `nodepool_rolling_upgrade_test.go` - Rolling upgrade tests
- `nodepool_autorepair_test.go` - Auto-repair functionality
- `nodepool_kv_*_test.go` - KubeVirt-specific tests
- `nodepool_machineconfig_test.go` - MachineConfig integration
- `nodepool_nto_*_test.go` - Node Tuning Operator tests

### Specialized Tests
- `autoscaling_test.go` - Cluster autoscaling tests
- `chaos_test.go` - Chaos engineering tests
- `external_oidc_test.go` - External OIDC authentication tests
- `olm_test.go` - Operator Lifecycle Manager tests

## Running Tests

### Build E2E Test Binary
```bash
make e2e
```

### Run Tests
Tests are built with the `e2e` build tag and require specific environment setup.

### Lint and Vet
```bash
make lint
make vet
```

## Test Utilities
The `util/` directory contains shared testing utilities and helper functions used across all e2e tests.

## Assets
The `assets/` directory contains test manifests, configurations, and other resources needed by the e2e tests.

## Development Notes
- All test files use the `//go:build e2e` build constraint
- Tests require proper AWS/Azure/OpenStack credentials and cluster access
- Many tests create real cloud resources and may incur costs
- Tests should clean up resources after completion

## Contributing
When adding new e2e tests:
1. Follow existing naming conventions (`*_test.go`)
2. Include appropriate build tags
3. Use shared utilities from the `util/` package
4. Add proper cleanup logic
5. Update this documentation if adding new test categories