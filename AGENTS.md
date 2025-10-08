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

## Troubleshooting Methodology

### Related Documentation

This section focuses on **investigation methodology and debugging patterns**. For operational guides and setup instructions, see:

- **Service Publishing Concepts**: [Exposing Services from HCP](docs/content/how-to/common/exposing-services-from-hcp.md)
- **External-DNS Setup**: [AWS External-DNS Guide](docs/content/how-to/aws/external-dns.md)
- **Operational Troubleshooting**: [General Troubleshooting](docs/content/how-to/troubleshooting-general.md)
- **Test Artifacts Reference**: [Test Artifacts Directory Structure](docs/content/reference/test-information-debugging/Azure/test-artifacts-directory-structure.md)

### Test Failure Analysis

When analyzing test failures, follow this systematic approach:

1. **Compare Successful vs Failed Runs**
   - Always find a successful run for comparison (check CI history or artifacts)
   - Identify what changed: OCP version, test configuration, platform settings
   - Look for differences in HostedCluster/HostedControlPlane status conditions
   - Compare route resources, service endpoints, and component logs

2. **Check Platform Type First**
   - Platform type fundamentally changes behavior: AWS, Azure, None, KubeVirt, etc.
   - Platform=None has specific limitations (no cloud provider integration)
   - Different platforms use different service publishing strategies
   - Check `hostedCluster.Spec.Platform.Type` before deep diving

3. **Understand Service Publishing Flow**
   ```
   HostedCluster spec.services
     ↓ (platform-specific creation in cmd/cluster/<platform>/create.go)
   Service Publishing Strategy (LoadBalancer, Route, NodePort)
     ↓ (control-plane-operator reconciliation)
   Route/Service Resources
     ↓ (external-dns or shared ingress processing)
   Infrastructure Ready
   ```

4. **Use Test Artifacts Documentation**
   - Reference: `docs/content/reference/test-information-debugging/Azure/test-artifacts-directory-structure.md`
   - Always start with HostedCluster, HostedControlPlane, and NodePool status
   - Check control plane component logs: kube-apiserver, etcd, oauth, etc.
   - Look for events and error messages in the component namespaces

5. **Identify False Positives**
   - Tests may pass for wrong reasons (incomplete validation, platform skips)
   - Check for version-gated validations (`AtLeast(t, VersionXXX)`)
   - Look for platform-specific skips in test code
   - New OCP versions may add validations that expose previously masked bugs
   - Example: `ValidateConfigurationStatus` added in 4.21 exposed Platform=None issues

### Route and Service Publishing Investigation

When debugging route or service accessibility issues:

> **Note**: For basic service publishing concepts (NodePort, LoadBalancer, Route strategies), see [Exposing Services from HCP](docs/content/how-to/common/exposing-services-from-hcp.md). This section focuses on **debugging route hostname issues**.

1. **Check Route Hostname Assignment Pattern**

   Routes can be created with hostnames in two ways:

   - **Pattern A (Explicit Hostname)**: Route has `spec.route.hostname` in HostedCluster spec
     - Used by: Cloud platforms (AWS, Azure) with `--external-dns-domain` flag
     - Processed by: external-dns controller (see [External-DNS Guide](docs/content/how-to/aws/external-dns.md))
     - Result: Route gets explicit hostname like `oauth-cluster.service-provider-domain.com`

   - **Pattern B (Auto-generated Hostname)**: No explicit hostname in spec
     - Used by: Platform=None, or when `--external-dns-domain` not provided
     - Depends on: `DEFAULT_INGRESS_DOMAIN` environment variable in control-plane-operator
     - Falls back to: **Empty hostname if neither is set** ⚠️

2. **Trace Route Creation Flow**
   ```
   cmd/cluster/<platform>/create.go
     ↓ (creates HostedCluster with service publishing strategy)
   hypershift-operator creates HostedControlPlane
     ↓
   control-plane-operator reconciles control plane
     ↓ (control-plane-operator/controllers/hostedcontrolplane/)
   Route resources created
     ↓ (support/util/route.go: ReconcileExternalRoute)
   Route.Spec.Host populated (or left empty)
   ```

3. **Understand External-DNS vs Shared Ingress** (Critical for Debugging)

   These are TWO different systems - knowing which one applies is essential:

   - **External-DNS** (cloud platforms only):
     - Only processes routes with **explicit hostnames** (Pattern A)
     - Creates DNS records in cloud provider (Azure DNS, Route53, etc.)
     - Does **NOT** process Platform=None routes
     - See setup guide: [AWS External-DNS](docs/content/how-to/aws/external-dns.md)

   - **Shared Ingress** (multi-tenant, all platforms):
     - HAProxy-based router watching labeled routes
     - Watches routes with label `hypershift.openshift.io/hosted-control-plane`
     - **Requires** routes to have `spec.host` field populated
     - **Skips routes without hostnames** - cannot route traffic without a hostname ⚠️

4. **Check Infrastructure Ready Dependencies**
   ```mermaid
   graph TD
     A[OAuth Route] --> B{Has spec.host?}
     B -->|Yes| C[Shared Ingress configures HAProxy]
     B -->|No| D[InfrastructureReady=False]
     C --> E[InfrastructureReady=True]
     E --> F[Kubeconfig Published]
     D --> G[Kubeconfig NOT Published]
   ```

### Code Navigation Patterns

1. **Platform-Specific Creation**
   - Start: `cmd/cluster/<platform>/create.go`
   - Look for: `ApplyPlatformSpecifics()`, service strategy mapping
   - Key function: `GetIngressServicePublishingStrategyMapping()`

2. **Controller Reconciliation**
   - HyperShift Operator: `hypershift-operator/controllers/hostedcluster/`
   - Control Plane Operator: `control-plane-operator/controllers/hostedcontrolplane/`
   - Component-specific: `control-plane-operator/controllers/hostedcontrolplane/<component>/`

3. **Utility Functions**
   - Route reconciliation: `support/util/route.go`
   - Service management: `support/util/service.go`
   - Platform utilities: `support/<platform>/`

4. **Shared Ingress**
   - Config generator: `sharedingress-config-generator/`
   - Watches routes and generates HAProxy configuration
   - Only processes routes with valid hostnames

### Common Investigation Pitfalls

- **Don't assume external-DNS processes all routes** - it only handles cloud platform routes with explicit hostnames
- **Check OCP version differences** - new versions add validations that may expose bugs
- **Platform=None is special** - it lacks cloud provider integration, has different service publishing
- **Validation skips may hide bugs** - check test code for platform-specific skips
- **Empty route hostnames are valid YAML** - but unusable by shared ingress
- **InfrastructureReady=False blocks everything** - trace back through its dependencies

### Quick Reference: Key Status Fields

Always check these when debugging:
- `HostedCluster.Status.Conditions[]` - Overall cluster health
- `HostedControlPlane.Status.Conditions[]` - Control plane component status
- `Route.Spec.Host` - Must be non-empty for shared ingress
- `Route.Status.Ingress[]` - Shows if route was processed
- Control plane operator environment: `DEFAULT_INGRESS_DOMAIN`

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