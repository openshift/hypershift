This file provides guidance when working with code in this repository.

## Repository Overview

HyperShift is a middleware for hosting OpenShift control planes at scale. It provides cost-effective and time-efficient cluster provisioning with portability across clouds while maintaining strong separation between management and workloads.

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

### API Versioning
- APIs primarily in v1beta1
- Use feature gates for experimental functionality
- CRD generation via controller-gen with OpenShift-specific tooling

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