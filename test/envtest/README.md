# HyperShift envtest API Validation

This directory contains YAML-driven integration tests that validate HyperShift CRD schemas
(including CEL validation rules) using [envtest](https://book.kubebuilder.io/reference/envtest).

Tests run against multiple Kubernetes and OCP API server versions to catch compatibility issues
across releases.

## How it works

1. Each test suite get its CRD installed and uninstalled before and after running.
2. Test suites are defined in cmd/install/assets/hypershift-operator/tests, as `{stable/techpreview}.{CRDName}.{suiteCase}.testsuite.yaml` files following the
   [openshift/api tests](https://github.com/openshift/api/tree/master/tests) format.
3. Each test case creates a resource from inline YAML and asserts either success or a specific validation error substring.

## Running

```bash
# Run against all supported OCP and vanilla Kubernetes versions
make test-envtest-api-all

# Run only OCP versions (4.17–4.22)
make test-envtest-ocp

# Run only vanilla Kubernetes versions (1.31–1.35)
make test-envtest-kube

# Run against a single version
make test-envtest-ocp ENVTEST_OCP_K8S_VERSIONS="1.34.1"

# These tests also run as part of `make test`
```

## Test format reference

The YAML format is compatible with
[openshift/api tests](https://github.com/openshift/api/tree/master/tests).
See [types.go](./types.go) for the Go type definitions.
