# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

This is a Go 1.24+ project using:
- Kubernetes 0.32.x APIs
- Controller-runtime 0.20.x
- Various cloud provider SDKs (AWS, Azure, IBM)
- OpenShift API dependencies

The project uses vendoring (`go mod vendor`) and includes workspace configuration in `hack/workspace/`.

## Common Gotchas

- Always run `make api` after modifying types in the `api/` package
- Use `make verify` before submitting PRs to catch formatting/generation issues
- Platform-specific controllers require their respective cloud credentials for testing
- E2E tests need proper cloud infrastructure setup (S3 buckets, DNS zones, etc.)

## Commit Messages

# Commit Message Formatting Rules

**When to apply**: When generating commit messages or discussing commit practices

## Commit Message Format

Use conventional commits format:

```
<type>(<scope>): <description>

[optional body]

Signed-off-by: <name> <email>
Assisted-by: <model-name>
```

### Commit Types

- **feat**: New features
- **fix**: Bug fixes
- **docs**: Documentation changes
- **style**: Code style changes
- **refactor**: Code refactoring
- **test**: Adding/updating tests
- **chore**: Maintenance tasks
- **build**: Build system or dependencies
- **ci**: CI/CD changes
- **perf**: Performance improvements
- **revert**: Revert previous commit

### Examples

```bash
feat(SRVKP-123): Add webhook controller for GitHub integration

Fixes an issue with the webhook controller for GitHub integration
```

```bash
fix(controller): Update pipeline reconciliation logic

Resolves issue with concurrent pipeline runs
```

### Gitlint Integration

- The project uses gitlint to enforce commit message format
- gitlint can be run by using this command `make run-gitlint`
- Ensure all commit messages pass gitlint validation
- Common gitlint rules to follow:
    - Conventional commit format
    - Proper line length limits
    - Required footers
    - No trailing whitespace