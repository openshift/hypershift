# HyperShift envtest API Validation

This directory contains YAML-driven integration tests that validate HyperShift CRD schemas
(including CEL validation rules) using [envtest](https://book.kubebuilder.io/reference/envtest).

Tests run against multiple Kubernetes and OCP API server versions to catch compatibility issues
across releases.

## How it works

1. **BeforeSuite** installs all HyperShift CRDs (hypershift-operator, cluster-api, all providers)
   into an envtest API server using the same `CustomResourceDefinitions()` function as `hypershift install`.
2. Test suites are defined as `.testsuite.yaml` files following the
   [openshift/api tests](https://github.com/openshift/api/tree/master/tests) format.
3. Each test case creates a resource from inline YAML and asserts either success or a specific
   validation error substring.

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

## Adding tests

Create or edit a `.testsuite.yaml` file in this directory. The format is:

```yaml
apiVersion: apiextensions.k8s.io/v1
name: "My CRD validation"
crdName: mycrd.hypershift.openshift.io
version: v1beta1
tests:
  onCreate:
  - name: When field X is invalid it should fail
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: MyCRD
      spec:
        fieldX: "bad-value"
        # ... include ALL required fields
    expectedError: "fieldX must be a valid ..."

  - name: When field X is valid it should pass
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: MyCRD
      spec:
        fieldX: "good-value"
        # ... include ALL required fields

  onUpdate:
  - name: When immutable field changes it should fail
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: MyCRD
      spec:
        immutableField: "original"
    updated: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: MyCRD
      spec:
        immutableField: "changed"
    expectedError: "immutableField is immutable"
```

### Rules

- Each test must include the **complete resource YAML**, not a diff against a base object.
- `expectedError` omitted or empty means the test expects creation/update to succeed.
- `expectedError` is matched as a **substring** of the actual error message.

## Test format reference

The YAML format is compatible with
[openshift/api tests](https://github.com/openshift/api/tree/master/tests).
See [types.go](./types.go) for the Go type definitions.
