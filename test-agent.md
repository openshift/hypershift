# Test Agent Guide

This file provides guidance to AI coding agents when running or writing tests in this repository.

## Quick Reference

| Test type | Command | Details |
|-----------|---------|---------|
| Unit tests | `make test` | Race-enabled, runs all packages |
| Single unit test | `go test -race -run TestName ./path/to/package/...` | Use `GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor` |
| E2E v2 | `make e2ev2` | Builds `bin/test-e2e-v2` |
| CEL envtest (OCP) | `make test-envtest-ocp` | Runs across supported k8s versions |
| CEL envtest (vanilla) | `make test-envtest-kube` | Vanilla k8s versions |
| Pre-PR gate | `make pre-commit` | Build + verify + test in one step |

## Conventions

- Place unit tests next to the code they test, in `_test.go` files within the same package.
- Name test functions `Test<FunctionName>` mapping 1:1 to the function under test.
- Name test cases `"When <condition>, it should <expected behavior>"`.
- Use real-world values in fixtures (e.g., `quay.io/openshift-release-dev/...`) instead of synthetic placeholders.
- Do not export functions only used in tests.

## Key Resources

- [TESTING.md](TESTING.md) — Full unit test conventions
- [test/e2e/v2/AGENTS.md](test/e2e/v2/AGENTS.md) — E2E v2 framework standards
- [test/envtest/README.md](test/envtest/README.md) — CEL validation test guide
- [DEVELOPMENT.md](DEVELOPMENT.md) — Build and test commands
