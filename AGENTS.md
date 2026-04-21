This file provides guidance when working with code in this repository.

## Repository Overview

HyperShift is a middleware for hosting OpenShift control planes at scale. It provides cost-effective and time-efficient cluster provisioning with portability across clouds while maintaining strong separation between management and workloads.

Project documentation is published via MkDocs. The site structure and navigation are defined in [docs/mkdocs.yml](docs/mkdocs.yml), with content under `docs/content/`. When adding or reorganizing documentation pages, update the `nav` section in `mkdocs.yml` to keep the site navigation in sync.

## Architecture

### Core Components
- **hypershift-operator**: Main operator managing HostedCluster and NodePool resources
- **control-plane-operator**: Manages control plane components for hosted clusters  
- **control-plane-pki-operator**: Handles PKI and certificate management
- **karpenter-operator**: Manages Karpenter resources for auto-scaling
- **ignition-server**: Serves ignition configs for node bootstrapping

### Key Directories
- `api/`: API definitions and CRDs for hypershift, scheduling, certificates, and karpenter
- `cmd/`: CLI commands for cluster/nodepool management and infrastructure operations
- `hypershift-operator/`: Main operator controllers and logic
- `control-plane-operator/`: Control plane component management
- `support/`: Shared utilities and libraries
- `test/`: E2E and integration tests

### Platform Support
The codebase supports multiple platforms:
- AWS (primary platform) - both self-managed and managed control plane (aka ROSA HCP)
- Azure - only managed control plane (aka ARO HCP)
- IBM Cloud (PowerVS)
- KubeVirt
- OpenStack
- Agent

### Private Cluster Ingress Design

Control plane (CP) ingress and guest cluster (data plane) ingress are two orthogonal concerns. They are handled by separate components and have separate implementations.

#### Control Plane Ingress
- Handled by HyperShift via the Private Link controllers.
- A dedicated `private-router` (HAProxy pod) is deployed in the Hosted Control Plane namespace on the management cluster.
- Exposed to the guest cluster's private network via a cloud-specific Private Link (AWS PrivateLink / Azure Private Link Service).
- Uses SNI-based routing to forward traffic to the appropriate CP service (KAS, OAuth, Konnectivity, Ignition).

#### Data Plane (Guest Cluster) Ingress
- Application traffic under `*.apps` is handled by the **ingress operator** running inside the guest cluster.
- This is standard OpenShift ingress, not something HyperShift's CP infrastructure manages.
- The CP `private-router` lives on the management side and **does not know how to resolve guest cluster workloads**.

#### Private Topology Scope
- A private topology (`endpointAccess: Private`) dictates how CP ingress endpoints are exposed (e.g. only via Private Link, not a public LB).
- It may also influence the desired visibility of guest cluster ingress, but the two are not inherently linked.

#### DNS: `hypershift.local` Zone
- DNS for private clusters uses a synthetic, non-configurable `<cluster-name>.hypershift.local` zone managed automatically by HyperShift.
- Records in this zone include:
  - `api.<cluster-name>.hypershift.local` → private endpoint IP
  - `*.apps.<cluster-name>.hypershift.local` → private endpoint IP
- The `*.apps` records in the `.hypershift.local` zone exist for CP-resident services exposed as routes (OAuth, Ignition, Konnectivity), **not** for guest cluster application traffic.
- The `.hypershift.local` `*.apps` wildcard is distinct from `*.apps.<cluster>.<basedomain>`. The former resolves CP service routes via the private endpoint. The latter is the guest cluster's application ingress domain, managed by the ingress operator on the data plane.

#### Historical Context: AWS `*.apps` in `hypershift.local`
- On AWS, both `api` and `*.apps` records exist in the `.hypershift.local` zone because initially there was support for KAS having its own LB, requiring two different private endpoints and therefore two distinct domain resolutions.
- A similar pattern may be needed in the future for Azure if OAuth gets its own LB, but that is a separate concern from guest cluster traffic routing.

#### Testing Guidance for Private Ingress Changes
- PRs modifying private DNS should validate traffic flow, not just DNS records.
- E2E tests should demonstrate that a traffic journey previously blocked is now enabled by the change, rather than simply asserting that DNS records exist in infrastructure.

## Development Commands

### Building
```bash
make build                    # Build all binaries
make hypershift-operator      # Build hypershift-operator
make control-plane-operator   # Build control-plane-operator
make hypershift               # Build CLI
```

### Testing
```bash
make test                     # Run unit tests with race detection
make e2e                      # Build E2E test binaries
make tests                    # Compile all tests
```

### Code Quality
```bash
make lint                     # Run golangci-lint
make lint-fix                 # Auto-fix linting issues
make verify                   # Full verification (generate, fmt, vet, lint, etc.)
make staticcheck              # Run staticcheck on core packages
make fmt                      # Format code
make vet                      # Run go vet
```

### API and Code Generation
```bash
make api                      # Regenerate all API resources and CRDs
make generate                 # Run go generate
make clients                  # Update generated clients
make update                   # Full update (api-deps, workspace-sync, deps, api, api-docs, clients)
```

### Development Workflow
```bash
make hypershift-install-aws-dev       # Install HyperShift for AWS development
make run-operator-locally-aws-dev     # Run operator locally for AWS development
bin/hypershift install --development # Install in development mode
bin/hypershift-operator run          # Run operator locally
```

## Testing Strategy

### Unit Tests
- Located throughout the codebase alongside source files
- Use race detection and parallel execution
- Run with `make test`

### E2E Tests
- Located in `test/e2e/`
- Platform-specific tests for cluster lifecycle
- Nodepool management and upgrade scenarios
- Karpenter integration tests
- v2 framework standards and patterns: see [test/e2e/v2/AGENTS.md](test/e2e/v2/AGENTS.md)

### Envtest (API Validation Tests)
- Located in `test/envtest/` with build tag `envtest`
- Tests CRD validation rules (CEL, OpenAPI schema) against real kube-apiserver + etcd
- Test cases are YAML-driven following the openshift/api convention
- Each YAML file defines `onCreate` and `onUpdate` test cases with expected errors
- Run with `make test-envtest-ocp` (OpenShift k8s versions) or `make test-envtest-kube` (vanilla k8s versions), or `make test-envtest-api-all` for both
- Tests run across multiple Kubernetes versions (1.31–1.35) to verify validation ratcheting and compatibility
- Feature gate filtering: test suites can target stable, tech-preview, or feature-gated CRD variants

See test/envtest/README.md for details

### Integration Tests
- Located in `test/integration/`
- Focus on controller behavior and API interactions

## Key Development Patterns

### Code quality, formatting and conventions
Please see /hypershift/.cursor/rules/code-formatting.mdc

### Operator Controllers
- Follow standard controller-runtime patterns
- Controllers in `hypershift-operator/controllers/` and `control-plane-operator/controllers/`
- Use reconcile loops with proper error handling and requeuing

### Platform Abstraction
- Platform-specific logic isolated in separate packages
- Common interfaces defined in `support/` packages
- Platform implementations in respective controller subdirectories

### Resource Management
- Use `support/upsert/` for safe resource creation/updates
- Follow owner reference patterns for proper garbage collection
- Leverage controller-runtime's structured logging

### CRD API Machinery Fundamentals

These are non-obvious behaviors of the Kubernetes API machinery that affect how CRD types in `api/` must be written. These are not style preferences or conventions — they are fundamental facts and reasoning about how the system works.

For conventions read https://github.com/openshift/enhancements/blob/master/dev-guide/api-conventions.md

`make api-lint-fix` will enforce most conventions and best practices.

#### API Versioning
- APIs primarily in v1beta1
- Any new API should GA as v1
- Use feature gates for experimental functionality
- CRD generation via controller-gen with OpenShift-specific tooling

#### Serialization
- **`omitempty` does nothing for non-pointer structs.** Only `omitzero` correctly omits a struct field when it equals its zero value. This is a Go encoding/json behavior, not a Kubernetes convention.
- **The only reason to use a pointer in a CRD is when the zero value is a valid, distinct user choice.** If the struct has a required field, `{}` can never be valid user input, so there is no ambiguity to resolve and no pointer is needed. `omitzero` on a non-pointer struct will correctly omit the key from serialized output. `MinProperties`/`MaxProperties` on the parent counts serialized keys — it has no concept of whether the Go field is a pointer.
- **`// +default` must be paired with `// +optional`** because the required check runs before defaulting. A required field with a default will be rejected before the default is ever applied.

#### Validation Execution
- **OpenAPI schema validation only runs when a key is present in the serialized object.** if a field is omitted, the validation never executes. This is why `MinLength=1` on an optional field is safe: the constraint only fires when the user actually provides a value.
- **Optionality and min constraints are independent concerns.** An optional field with `MinLength=1` means "you don't have to set this, but if you do, it can't be empty." These do not conflict.
- **Admission-time validation rejects the write immediately.** Controller-time validation accepts the write, the user assumes success, and discovers the problem later via conditions or logs. Always prefer admission-time via CEL over controller-time validation.

#### Immutability
- **Immutable + optional creates a two-step bypass.** A user can (1) remove the optional field, then (2) re-add it with a different value. To prevent this, add a CEL rule at the parent level that forbids removing the field once set: `oldSelf.has(field) ? self.has(field) : true`.
- **"Once set, cannot be removed" and "once set, cannot be changed" are separate constraints.** You typically need both together, and they require separate CEL rules.

#### Defaulting and Transitions
- **Ratcheting validation**: when adding new validation to existing fields, verify that existing clusters with values that predate the new validation can still be updated. CRD validation ratchets (allows unchanged invalid values through), but only for fields that are literally unchanged in the update.

## Dependencies and Modules

This is a Go 1.25+ project using:
- Kubernetes 0.34.x APIs
- Controller-runtime 0.22.x
- Various cloud provider SDKs (AWS, Azure, IBM)
- OpenShift API dependencies

The project uses vendoring (`go mod vendor`) and includes workspace configuration in `hack/workspace/`.

### Multi-Module Structure

This repository contains **multiple Go modules**. The `api/` directory is a **separate Go module** with its own `api/go.mod` (module path: `github.com/openshift/hypershift/api`). The main module at the repository root consumes the `api/` module through vendoring.

This means:
- Edits to files under `api/` (e.g. `api/hypershift/v1beta1/`) are **not visible** to the main module until the vendored copy is updated.
- After modifying any types, constants, or functions in `api/`, you **must** run `make update` to regenerate CRDs, revendor dependencies, and sync everything. `make update` runs the full sequence: `api-deps` → `workspace-sync` → `deps` → `api` → `api-docs` → `clients` → `docs-aggregate`. Without this, the main module build will fail with `undefined` errors for any new symbols added in `api/`.
- **Do not modify `vendor/` directories directly.** The `vendor/` directories are managed by `go mod vendor` (via `make deps` and `make api-deps`). Always use `make update` to keep them in sync.
- Running `go build ./...` or `go vet ./...` from the repository root will **not** compile the `api/` module — it is a separate module. To build/vet the API module, run commands from within the `api/` directory.
- The `hack/workspace/` directory contains a Go workspace configuration (`go.work`) that can be used for local development across both modules.

## Common Gotchas

- **`api/` is a separate Go module**: Always run `make update` after modifying types in the `api/` package. See [Multi-Module Structure](#multi-module-structure) above for details.
- **Do not modify `vendor/` directories directly**: They are managed by `go mod vendor` via `make update`.
- Use `make verify` before submitting PRs to catch formatting/generation issues.
- Platform-specific controllers require their respective cloud credentials for testing.
- E2E tests need proper cloud infrastructure setup (S3 buckets, DNS zones, etc.).

## Commit Messages
Please see /hypershift/.cursor/rules/git-commit-format.mdc for information on how commit messages should be generated or formatted
in this project.

### Gitlint Integration

- The project uses gitlint to enforce commit message format
- gitlint can be run by using this command `make run-gitlint`
- Ensure all commit messages pass gitlint validation
- Common gitlint rules to follow:
    - Conventional commit format
    - Proper line length limits
    - Required footers
    - No trailing whitespace

## Code conventions
- Prefer Gherkin Syntax to define unit test cases, e.g. "When... it should..."
- Prefer gomega for unit test assertions
