# API Dependencies Verification Tool

This tool enforces dependency restrictions for the separate HyperShift API module
(`api/`).

## Enforced Invariants

### Direct dependency allowlist

The API module may directly require only the modules listed in
`api/.imports_allowed`:

- Core Kubernetes APIs (`k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/utils`)
- OpenShift API definitions (`github.com/openshift/api`)

Indirect requirements are excluded from this allowlist check because Go manages
them transitively. The allowlist lives beside `api/go.mod`, falls under API
reviewer ownership, supports comments beginning with `#`, and contains one module
path per line.

### Shared dependency versions

Every module path required by both root `go.mod` and `api/go.mod` must use the
same literal version. This comparison:

- Includes both direct and indirect requirements
- Does not require direct/indirect classifications to match
- Ignores dependencies required by only one module
- Reports every mismatch in deterministic module-path order

This removes literal shared-require drift, which is one source of divergent
module graphs.

### Shared dependency replacements

For paths literally required by both modules, every replacement directive must
match in both files. The comparison uses the union of all replacement keys,
including inactive version-scoped keys. An unversioned key does not match a
version-scoped key. Module targets must have the same path and version.

Local filesystem targets are resolved relative to the `go.mod` containing the
directive and compared as lexically cleaned absolute paths. Symlinks are not
resolved. This allows different relative spellings of the same location while
keeping diagnostics explicit about both the literal and resolved paths.

Requirements and replacements for paths unique to one module are outside this
check. That includes root-only local use of the HyperShift API module and
root-only Karpenter replacements. Replace-only or transitive paths that are not
literal requirements in both modules are also outside the check. Exclude
directives are intentionally not checked for symmetry.

These literal directive checks do not prove effective MVS graph equality, check
every transitive selection, or prove that a downstream consumer can resolve the
API module. Standalone graph and consumer tests cover those contracts
separately.

## Usage

Run the supported local check from the repository root:

```bash
make verify-api-deps
```

The target runs the verifier's unit tests, rebuilds the verifier when its source
changes, validates the API dependency allowlist, and compares shared requirement
versions and replacements.

The check also runs through `make verify`, `make verify-ci`, pre-commit
verification, and the repository's verify CI workflow.

## Mismatch Diagnostics

A shared requirement or replacement mismatch fails with the key and value
declared by each file:

```text
shared dependency versions do not match:
  example.com/a
    go.mod:     v1.2.0
    api/go.mod: v1.1.0

shared dependency replacements do not match:
  example.com/b <all versions>
    go.mod:     => example.com/b-fork v1.2.3
    api/go.mod: <missing>

align shared requirements and replacements and run `make update`
```

Align all reported shared requirements and replacements, then run `make update`
to refresh module metadata, vendoring, and generated artifacts according to
repository conventions.

## Adding API Dependencies

Before adding a new direct dependency to the API module:

1. Consult API reviewers about alternatives and necessity.
2. Ensure the dependency is essential for API type definitions.
3. Verify compatibility and downstream impact.
4. After approval, add its module path to `api/.imports_allowed`.
5. If the root module already requires it, use the same literal version.
6. Align any replacement keys for paths required by both modules.
7. Run `make update` and `make verify-api-deps`.

Dependencies unique to one module do not need to be introduced into the other
module. If a dependency later becomes shared, the consistency check begins
enforcing its version automatically.

## Rationale

The direct dependency allowlist maintains a minimal, stable API dependency
surface. Shared literal-requirement and replacement enforcement makes dependency
updates explicit across the two modules and prevents silent directive drift
while preserving each module's unique requirements.
