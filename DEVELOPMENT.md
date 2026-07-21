# Development

## Key Make Targets

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
make e2ev2                    # Build v2 E2E test binary (bin/test-e2e-v2)
make tests                    # Compile all tests (no execution)
make test-envtest-ocp         # Run envtest for CEL validations (OpenShift k8s versions)
make test-envtest-kube        # Run envtest for vanilla k8s versions
make test-envtest-api-all     # Run envtest for both
```

To run a single unit test or package:
```bash
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race -run TestName ./path/to/package/...
```

To run envtest against a single k8s version:
```bash
ENVTEST_OCP_K8S_VERSIONS=1.35.0 make test-envtest-ocp
```

To run envtest versions in parallel:
```bash
ENVTEST_JOBS=MAX make test-envtest-ocp     # All versions in parallel
ENVTEST_JOBS=3 make test-envtest-ocp       # Up to 3 versions in parallel
```

To run envtest for a single suite, use Ginkgo's `--focus` flag:
```bash
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -tags envtest -race ./test/envtest/... -- --focus="hostedclusters.*etcd"
```

### Code Quality

```bash
make lint                     # Run golangci-lint
make lint-fix                 # Auto-fix linting issues
make verify                   # Full verification (generate, update, staticcheck, fmt, vet, lint, codespell, gitlint)
make staticcheck              # Run staticcheck on core packages
make fmt                      # Format code
make vet                      # Run go vet
make verify-codespell         # Catch spelling errors in markdown
make run-gitlint              # Validate commit message format
make pre-commit               # Full pre-PR gate (build, e2e compile, verify, test)
```

### API and Code Generation

```bash
make api                      # Regenerate all CRDs, deepcopy, clients
make api-lint-fix             # Run API linter and auto-fix violations
make generate                 # Run go generate (regenerates *_mock.go files in place)
make clients                  # Update generated clients
make update                   # Full update (api-deps, workspace-sync, capi-sync, deps, api, ...)
make capi-sync                # Sync CAPI provider types (incremental)
make capi-sync-force          # Force re-sync all CAPI providers
make verify-capi-sync         # Verify CAPI sync is clean (CI target)
```

## Development Patterns

### Resource Management

Use `support/upsert/` for safe resource creation and updates. Follow owner reference patterns for proper garbage collection.

### Operator Controllers

Controllers follow standard controller-runtime reconcile loop patterns. Locations:

- `hypershift-operator/controllers/` — HostedCluster and NodePool reconciliation
- `control-plane-operator/controllers/` — control plane component reconciliation (v2 framework)

### Platform Abstraction

Platform-specific logic is isolated in separate packages. Common interfaces are defined in `support/` packages, with platform implementations in respective controller subdirectories.

## Multi-Module Structure

This repository contains **multiple Go modules**. The `api/` directory is a **separate Go module** with its own `api/go.mod` (module path: `github.com/openshift/hypershift/api`). The main module at the repository root consumes the `api/` module through vendoring.

This means:

- Edits to files under `api/` (e.g. `api/hypershift/v1beta1/`) are **not visible** to the main module until the vendored copy is updated.
- After modifying any types, constants, or functions in `api/`, you **must** run `make update` to regenerate CRDs, revendor dependencies, and sync everything. `make update` runs the full sequence: `api-deps` → `workspace-sync` → `capi-sync` → `deps` → `api` → `api-docs` → `clients` → `docs-aggregate`. Without this, the main module build will fail with `undefined` errors for any new symbols added in `api/`.
- **Do not modify `vendor/` directories directly.** The `vendor/` directories are managed by `go mod vendor` (via `make deps` and `make api-deps`). Always use `make update` to keep them in sync.
- Running `go build ./...` or `go vet ./...` from the repository root will **not** compile the `api/` module — it is a separate module. To build/vet the API module, run commands from within the `api/` directory.
- The `hack/workspace/` directory contains a Go workspace configuration (`go.work`) that can be used for local development across both modules.

### CAPI Provider Types

Upstream CAPI infrastructure provider types (e.g. `sigs.k8s.io/cluster-api-provider-aws/v2`) are **not vendored directly**. Instead, they are copied into local `pkg/capi/<provider>/` modules using an AST-based tool that strips declarations depending on banned imports. This decouples HyperShift's `go.mod` from the full transitive dependency tree of each CAPI provider.

Each `pkg/capi/<provider>/` directory is a separate Go module with its own `go.mod`. The main module consumes them via `replace` directives in `go.mod`.

Key commands:

```bash
make capi-sync                # Sync all CAPI provider types (incremental, stamp-based)
make capi-sync-force          # Force re-sync all providers from scratch
make cluster-api-provider-aws # Generate CRDs for a specific CAPI provider
```

The sync process (`hack/capi-sync-provider.sh`) for each provider:
1. Downloads the upstream module into the Go module cache via `go mod download`
2. Copies types from the cached module into `pkg/capi/<provider>/` using `hack/copy-capi-types/main.go`, which strips functions and types that reference banned imports (e.g. controller-runtime, cloud SDKs)
3. Generates deepcopy functions using `controller-gen` inside a temporary Go workspace

CRD generation (`hack/capi-workspace-run.sh`) similarly creates a temporary `go.work` on the fly to resolve types across the main module and `pkg/capi/<provider>` modules. No committed workspace file is required.

To add a new CAPI provider or update an existing one:
1. Create or update `hack/capi-vendor/<provider>/go.mod` with the desired upstream version
2. Run `make capi-sync-force` to download the module and regenerate all local copies
3. Run `make verify-capi-sync` to confirm the sync is clean

## Pre-PR Gate

Run `make pre-commit` before submitting a PR. It executes the full sequence: build, e2e compile, verify (formatting, linting, gitlint), and unit tests. This is the single command that catches most CI failures locally.

## Go Version

The minimum Go version is declared in [`go.mod`](go.mod). The `api/` module uses `omitzero` struct tags (available since Go 1.24) and other features that require this minimum version.

## Common Gotchas

- **`api/` is a separate Go module**: Always run `make update` after modifying types in the `api/` package. See [Multi-Module Structure](#multi-module-structure) above for details.
- **Do not modify `vendor/` directories directly**: They are managed by `go mod vendor` via `make update`.
- Use `make verify` before submitting PRs to catch formatting/generation issues.
- Platform-specific controllers require their respective cloud credentials for testing.
- E2E tests need proper cloud infrastructure setup (S3 buckets, DNS zones, etc.).
- `make generate` regenerates `*_mock.go` files in place via `go generate` — don't hand-edit mock files.
- **No unrelated changes in PRs**: Do not include cosmetic formatting, whitespace, or import reordering changes in files unrelated to the PR's purpose. Unrelated changes increase review surface and make PRs harder to revert cleanly. If you notice something worth cleaning up, do it in a separate PR.
- **Follow existing codebase patterns**: Before implementing a new approach (e.g., using service-ca operator for TLS), search the codebase for how similar problems are already solved (e.g., `reconcileSelfSignedCA`). HyperShift self-signs certificates — use the existing self-signing pattern instead of relying on external certificate operators.

## Jira Integration

- **Features/epics/stories/tasks**: Create in the **CNTRLPLANE** project (Red Hat OpenShift Control Planes)
- **Bugs**: Create in the **OCPBUGS** project (OpenShift Bugs)
- **Components**: Use `HyperShift / ARO` for ARO HCP, `HyperShift / ROSA` for ROSA HCP, or `HyperShift` when platform is unclear

## Commit Messages

Use conventional commit format. Validate with `make run-gitlint`. Do NOT put Jira IDs in commit messages — they belong only in PR titles.

```
<type>(<scope>): <description>

[optional body]

Signed-off-by: <name> <email>
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`, `revert`. Title max 120 chars, body lines max 140 chars. Include a `Signed-off-by` footer — get name/email from `git config user.name` and `git config user.email`.

Use the `git-commit-format` skill for full details and examples.

### Restructuring Commits Before PR Submission

Before creating a PR or after addressing review comments, use the `restructure-commits` skill to reorganize all branch commits into logical, component-based commits. This ensures every PR has a clean, reviewable commit history grouped by architectural boundary.

## Pull Requests

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full contribution guidelines. Key points for agents:

### Before Creating a PR

1. Use the `restructure-commits` skill to organize commits by component (see [Restructuring Commits](#restructuring-commits-before-pr-submission) above)
2. Run `make pre-commit` to build, compile e2e tests, run verification (formatting, linting, gitlint), and run unit tests

### PR Title

Prefix with a Jira ticket number: `OCPBUGS-12345: Fix memory leak in controller`. Use `NO-JIRA:` only when no Jira issue exists (sparingly).

### PR Description

Follow the template in `.github/PULL_REQUEST_TEMPLATE.md`.

### PR Workflow

1. Open the PR in **draft mode** to avoid triggering all CI jobs and notifying approvers
2. Run necessary CI jobs manually with `/test <job-name>`
3. Mark as "Ready for Review" once tests pass and required labels are applied

### After Review Comments

After addressing review feedback, use the `restructure-commits` skill again to reorganize commits before force-pushing. This keeps the commit history clean for subsequent review rounds.

## Code Conventions

For unit test creation requirements, naming conventions, and placement rules, see [TESTING.md](TESTING.md).

Additional review-derived rules:

- Do not leave dead code (functions defined but never called). Remove unused code before submitting.
- Do not leave TODO comments in validation regex patterns or CEL rules — resolve them before submitting. Reviewers have blocked PRs for shipping regex patterns with placeholder character classes (e.g., allowing `{` and `}` in UUID fields, or missing anchoring constraints).
- When writing regex for API validation, match the upstream format exactly. For UUIDs, use `[0-9a-f]{8}-...`; for Azure resource names, verify the allowed character set against Azure documentation. Do not over-broaden patterns with catch-all classes like `[a-zA-Z0-9-_().{}]`.
