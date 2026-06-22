# HyperShift envtest API Validation

This directory contains YAML-driven integration tests that validate HyperShift CRD schemas
(including CEL validation rules) using [envtest](https://book.kubebuilder.io/reference/envtest).

Tests run against multiple Kubernetes and OCP API server versions to catch compatibility issues
across releases.

## Directory layout

```
cmd/install/assets/crds/hypershift-operator/
├── zz_generated.crd-manifests/                    # Generated CRDs (by make api)
│   ├── 0000_10_hostedclusters-Default.crd.yaml
│   ├── 0000_10_hostedclusters-TechPreviewNoUpgrade.crd.yaml
│   └── ...
├── tests/                                         # Test suite YAMLs
│   ├── hostedclusters.hypershift.openshift.io/
│   │   ├── stable.hostedclusters.validation.testsuite.yaml
│   │   └── ...
│   └── nodepools.hypershift.openshift.io/
│       └── ...
└── payload-manifests/                             # Feature gate definitions
    └── featuregates/
```

The framework resolves CRDs relative to the test file: from `tests/<crd-group>/`, it navigates `../../zz_generated.crd-manifests/` to find the CRD to install. Feature gates are resolved the same way via `../../payload-manifests/featuregates/`. This means test YAMLs must stay in `tests/` as siblings of `zz_generated.crd-manifests/`.

This layout is generic — it works with any directory that has the right sibling structure (`zz_generated.crd-manifests/` and `tests/` as siblings). The framework resolves everything via relative paths from the test YAML, so the root can live anywhere in the repo. `LoadTestSuiteSpecs` accepts multiple root paths, so to add tests for other APIs (e.g., karpenter), create the same layout under the relevant asset directory and add the path in `suite_test.go`:

```go
suites, err = LoadTestSuiteSpecs(assetsDir, karpenterDir)
```

## How it works

1. Each test suite gets its CRD installed and uninstalled before and after running.
2. Test suites are defined in `cmd/install/assets/crds/hypershift-operator/tests/`, as `{stable/techpreview}.{CRDName}.{suiteCase}.testsuite.yaml` files following the
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

### Parallel execution

By default, versions run sequentially. Use `ENVTEST_JOBS` to run multiple versions
in parallel — each version gets its own isolated envtest environment (etcd + kube-apiserver):

| Value | Behaviour |
|-------|-----------|
| `0` (default) | Sequential — one version at a time |
| `N` | Run up to N versions in parallel |
| `MAX` | Run all versions in parallel |

```bash
# Run 3 OCP versions in parallel
make test-envtest-ocp ENVTEST_JOBS=3

# Run all OCP versions in parallel
make test-envtest-ocp ENVTEST_JOBS=MAX

# Run all Kubernetes versions in parallel
make test-envtest-kube ENVTEST_JOBS=MAX

# Works with the combined target too
make test-envtest-api-all ENVTEST_JOBS=MAX
```

## Test format reference

The YAML format is compatible with
[openshift/api tests](https://github.com/openshift/api/tree/master/tests).
See [types.go](./types.go) for the Go type definitions.
