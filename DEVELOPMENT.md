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

### Code Quality

```bash
make lint                     # Run golangci-lint
make lint-fix                 # Auto-fix linting issues
make verify                   # Full verification (generate, fmt, vet, lint, etc.)
make staticcheck              # Run staticcheck on core packages
make fmt                      # Format code
make vet                      # Run go vet
make pre-commit               # Full pre-PR gate (update, build, verify, test, gitlint)
```

### API and Code Generation

```bash
make api                      # Regenerate all CRDs, deepcopy, clients
make api-lint-fix             # Run API linter and auto-fix violations
make generate                 # Run go generate
make clients                  # Update generated clients
make update                   # Full update (api-deps, workspace-sync, deps, api, api-docs, clients, docs-aggregate)
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
- **No unrelated changes in PRs**: Do not include cosmetic formatting, whitespace, or import reordering changes in files unrelated to the PR's purpose. Unrelated changes increase review surface and make PRs harder to revert cleanly. If you notice something worth cleaning up, do it in a separate PR.
- **Follow existing codebase patterns**: Before implementing a new approach (e.g., using service-ca operator for TLS), search the codebase for how similar problems are already solved (e.g., `reconcileSelfSignedCA`). HyperShift self-signs certificates — use the existing self-signing pattern instead of relying on external certificate operators.

## Commit Messages

Use the `git-commit-format` skill for formatting rules and required footers. Validate with `make run-gitlint`. Do NOT put Jira IDs in commit messages — they belong only in PR titles.

### Restructuring Commits Before PR Submission

Before creating a PR or after addressing review comments, use the `restructure-hypershift-commits` skill to reorganize all branch commits into logical, component-based commits. This ensures every PR has a clean, reviewable commit history grouped by architectural boundary.

## Pull Requests

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full contribution guidelines. Key points for agents:

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

## Code Conventions

For test naming and formatting rules, see `.claude/skills/code-formatting`.

Additional review-derived rules:

- Do not export functions that are only used in tests. Use unexported helpers or keep them in `_test.go` files.
- Do not leave dead code (functions defined but never called). Remove unused code before submitting.
- When adding new exported functions or methods, cover them with unit tests. For controller reconciliation methods, test at minimum: happy path, missing/empty input, disabled capability, and the primary error path.
- Do not leave TODO comments in validation regex patterns or CEL rules — resolve them before submitting. Reviewers have blocked PRs for shipping regex patterns with placeholder character classes (e.g., allowing `{` and `}` in UUID fields, or missing anchoring constraints).
- When writing regex for API validation, match the upstream format exactly. For UUIDs, use `[0-9a-f]{8}-...`; for Azure resource names, verify the allowed character set against Azure documentation. Do not over-broaden patterns with catch-all classes like `[a-zA-Z0-9-_().{}]`.
- Use real-world values in test fixtures when possible (e.g., `quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64` instead of `example.com/image:latest`). Real values catch edge cases that synthetic values miss.
- When adding test assertions for OwnerReferences, check that they were actually set during reconciliation — not just that the object exists.
