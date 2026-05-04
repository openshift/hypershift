This file provides guidance when working with code in this repository.

## Repository Overview

HyperShift is a middleware for hosting OpenShift control planes at scale. It provides cost-effective and time-efficient cluster provisioning with portability across clouds while maintaining strong separation between management and workloads.

Project documentation is published via MkDocs. The site structure and navigation are defined in [docs/mkdocs.yml](docs/mkdocs.yml), with content under `docs/content/`. When adding or reorganizing documentation pages, update the `nav` section in `mkdocs.yml` to keep the site navigation in sync.

## Architecture

### Core Components

- **hypershift-operator**: Main operator managing HostedCluster and NodePool resources
- **control-plane-operator**: Manages control plane components for hosted clusters. See support/controlplane-component/README.md
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
- Azure - both self-managed and managed control plane (aka ARO HCP)
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

See .claude/skills/dev

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
See api/AGENTS.md

### Design Invariants

See [docs/content/reference/goals-and-design-invariants.md](docs/content/reference/goals-and-design-invariants.md) for security and architectural invariants that all code changes must respect.

### Versioning

See [docs/content/reference/versioning-support.md](docs/content/reference/versioning-support.md) for release cycle, version skew policies, and support matrices for the HyperShift Operator, CPO, and other components.

### Upgrades lifecycle

See [docs/content/how-to/upgrades.md](docs/content/how-to/upgrades.md) for Control Plane and Data Plane upgrades

### Pull secret cycling

When changing how workers and the guest cluster authenticate to registries, treat **HostedCluster** `spec.pullSecret`, **management-cluster** Secret data, **HCCO** reconciliation into the data plane, and optional **Global Pull Secret** (`kube-system/additional-pull-secret`) as one system. Changing only the Secret bytes in place does not change `spec.pullSecret` and therefore does not drive a **NodePool** rollout, but controllers can still propagate credentials into the control plane namespace, guest `openshift-config`, `kube-system/original-pull-secret`, and (where the Global Pull Secret DaemonSet is scheduled) kubelet configuration.

See [docs/content/how-to/common/global-pull-secret.md](docs/content/how-to/common/global-pull-secret.md) for behavior, platform and NodePool eligibility (AWS/Azure Replace vs InPlace and other platforms), merge semantics, and operational guidance.

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

Use the `git-commit-format` skill for formatting rules and required footers. Validate with `make run-gitlint`. Do NOT put Jira IDs in commit messages — they belong only in PR titles.

### Restructuring Commits Before PR Submission

Before creating a PR or after addressing review comments, use the `restructure-hypershift-commits` skill to reorganize all branch commits into logical, component-based commits. This ensures every PR has a clean, reviewable commit history grouped by architectural boundary.

## Pull Requests

See [CONTRIBUTING.md](.github/CONTRIBUTING.md) for the full contribution guidelines. Key points for agents:

### Before Creating a PR

1. Use the `restructure-hypershift-commits` skill to organize commits by component (see [Restructuring Commits](#restructuring-commits-before-pr-submission) above)
2. Run `make pre-commit` to update dependencies, build, verify formatting, run tests, and validate commit messages via gitlint

### PR Title

Prefix with a Jira ticket number: `OCPBUGS-12345: Fix memory leak in controller`. Use `NO-JIRA:` only when no Jira issue exists (sparingly).

### PR Description

Follow the template in `.github/PULL_REQUEST_TEMPLATE.md`.

### PR Workflow

1. Open the PR in **draft mode** to avoid triggering all CI jobs and notifying approvers
2. Run necessary CI jobs manually with `/test <job-name>`
3. Mark as "Ready for Review" once tests pass and required labels are applied

### After Review Comments

After addressing review feedback, use the `restructure-hypershift-commits` skill again to reorganize commits before force-pushing. This keeps the commit history clean for subsequent review rounds.

## Code conventions

- Prefer Gherkin Syntax to define unit test cases, e.g. "When... it should..."
- Prefer gomega for unit test assertions

