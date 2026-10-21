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
make deadcode                 # Report whole-program dead-code candidates (not a gate)
make fmt                      # Format code
make vet                      # Run go vet
make verify-codespell         # Catch spelling errors in markdown
make run-gitlint              # Validate commit message format across a commit range
```

#### Dead-code reporting

Run a reporting-only scan with the supported Go toolchain from the repository root:

```bash
make deadcode ARTIFACT_DIR="$PWD/.work/deadcode"
```

The target builds `golang.org/x/tools/cmd/deadcode` pinned to v0.44.0 from the
vendored tools module, builds the report helper, and runs `generate` for ignored
mocks. It does not modify tracked source files or delete candidates. It is not
part of `verify`, `verify-ci`, or `pre-commit`, and does not enforce a baseline.

Analysis loads root-module `./...`, all executable entry points, and test
executables (`-test`) using tags `integration,e2e,reqserving,e2ev2,backuprestore`.
It uses `GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor`, `GOOS=linux`,
`GOARCH=arm64`, and `CGO_ENABLED=0`, even on a different developer host. Tools
and mock generation explicitly use `GOHOSTOS`/`GOHOSTARCH`, not inherited or
persisted cross-compilation targets. Before generation, the target rebuilds
`mockgen` natively even if a cached foreign-target executable is newer than its
Make prerequisites. `GOPACKAGESDRIVER=off` ensures standard Go
package loading rather than an inherited or automatically discovered driver.
Separate modules under `api/`,
`hack/tools/`, and nested `contrib/` directories are not independent scan targets.

The initial scan deliberately excludes the `envtest` tag:
`test/envtest/generator.go` references `cfg`, `k8sClient`, and `ctx` declared only
in `suite_test.go`, so deadcode cannot load the non-test package variant with
that tag enabled. Other loader/type-check errors are failures, not exclusions.

The artifact directory (default `/tmp/artifacts`) receives:

| File | Contract |
|------|----------|
| `deadcode.json` | Sorted array of records with `package`, `function` (including method receiver), repository-relative `file`, and 1-based declaration `line`/`column`. Empty results are `[]`. |
| `deadcode.txt` | The same candidates as readable `file:line:column: package.function` records; empty when there are no candidates. |
| `deadcode-summary.txt` | Completion summary with commit, tracked dirty state, analyzer/Go versions, environment, tags, exclusions, elapsed analysis time, memory settings, and totals. |

Records are sorted by package/function/file before location, so package/function
identity is useful independently of line-number changes. Generated declarations,
all vendor sources (including the vendored HyperShift API), copied
`support/thirdparty/` code, and sources outside the root module are excluded from
reported findings, **not** from dependency loading or reachability analysis.
Non-generated test declarations in the root module remain eligible findings.

Completed analysis exits zero both with candidates and without them. Tool-build,
mock-generation, package/type-check, malformed-output, and report-writing errors
exit nonzero with diagnostic logs; do not suppress these failures. The helper
invalidates old reports before scanning, stages new reports, and publishes the
completion summary last. Use a fresh artifact directory for each run: a failed
Make prerequisite can leave artifacts from an earlier invocation untouched.

The analyzer defaults to `GOMEMLIMIT=6GiB` and `GOGC=50`; explicit environment
values override these defaults and are recorded in the summary. A Go memory
limit is **not** an RSS or container memory limit. Whole-program analysis can
require substantial memory. CI scheduling, resources, artifact publication, and
runtime/memory measurements are owned by the separate CNTRLPLANE-4601 task.

Candidates require human review, not automatic deletion. An unreachable method
may still be required to satisfy an interface and cannot necessarily be deleted
individually. Public APIs can have downstream callers absent from this
repository. Results apply only to the selected platform/tags and executable
roots; other configurations may reach the same code. The analyzer also does not
fully understand `go:linkname` aliases. See the [deadcode documentation](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode)
for algorithm and interpretation limits.

##### Analyzer executable trust boundary

`deadcode-report` always runs the `deadcode` executable next to its own binary.
`make deadcode` builds both binaries together from the pinned, vendored tools
module. There is no analyzer-path CLI override or analyzer lookup through `PATH`.
This local developer/CI tool runs with the invoking user's privileges; it is not
a sandbox for analyzing untrusted checkouts or binaries. The executables, their
parent directories/symlink targets, checkout, Go/Git tools, and inherited
environment must remain trusted for the entire run.

The helper converts the sibling path to an absolute filename and checks its Go
build metadata for the analyzer's command/module identity and v0.44.0 version.
Those checks catch incompatible tools; they are **not** signature verification,
a cryptographic authenticity guarantee, or protection against concurrent
replacement. Execution uses `exec.CommandContext` directly with separate
arguments, never a shell. Spaces and shell metacharacters in the filename are
literal, as covered by the offline reachability fixture.

Run the helper's offline fixture checks explicitly because root-module tests do
not cover the tools module:

```bash
cd hack/tools
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race ./deadcode-report
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

A separate `go.mod` is not on its own enough to decouple a provider from CAPI. Go resolves one version of a module per build, so copied types that still imported `sigs.k8s.io/cluster-api` would compile against whatever version the **root** module selected, regardless of the provider's own `go.mod` — and the provider's requirement would act as a floor on the root, not a ceiling on itself. That also made each provider's generated CRD schema a function of the root CAPI version rather than of the version its controller was built against.

So the sync extracts the CAPI declarations each provider actually needs into a `pkg/capi/<provider>/capicore/` package and repoints the imports at it, leaving the provider modules with **no dependency on `sigs.k8s.io/cluster-api` at all**. The extraction is driven by the CAPI version that provider resolves to in `hack/capi-vendor/<provider>/`, which differs between providers today, so each can move independently and the root module's CAPI version can move without touching any of them. Only declarations reachable from type, const and var declarations are extracted, since those are what shape the generated CRDs; a function reaching further into CAPI is stripped rather than dragging a much larger type closure along.

Only **core CAPI** is decoupled this way. The copied types still depend on shared modules such as `k8s.io/api`, `k8s.io/apimachinery` and `sigs.k8s.io/controller-runtime`, and a Go build still resolves exactly one version of each of those: through the `replace` directives, each provider module's requirements participate in the main module's MVS, and the same holds for the temporary `go.work` used for CRD generation. Those requirements are floors on the root, not ceilings on the providers — every provider's copied code compiles against whichever shared version wins, not the one its own `go.mod` picked. A breaking change in a shared module (e.g. `k8s.io/apimachinery` removing an API one provider's copied types still use) can therefore still fail the build even though the providers' CAPI versions are independent. The exposure is limited in practice, because the kept declarations are mostly struct definitions leaning on stable `meta/v1` machinery — but when it does bite, the fix is to bump the affected provider in `hack/capi-vendor/<provider>/go.mod` to a version built against the newer shared module.

Key commands:

```bash
make capi-sync                # Sync all CAPI provider types (incremental, stamp-based)
make capi-sync-force          # Force re-sync all providers from scratch
make capi-sync-aws            # Re-sync one provider
make cluster-api-provider-aws # Generate CRDs for a specific CAPI provider
make verify-capi-decoupling   # Check the invariants below without a full CI run
```

Because each provider is a separate module reached through `replace`, `go build ./...` in the root module compiles only the provider subpackages something imports — a broken `exp/api` package stays invisible until CRD generation. It also means the localized `capicore` types and the upstream CAPI types they were copied from are **distinct Go types**: code merged from upstream that names `clusterv1beta1` or `capiv1` where a file already uses `capicore` will not compile, and the error does not say why.

`make verify-capi-decoupling` (`hack/verify-capi-decoupling.sh`) checks this in seconds: no provider module may require or import `sigs.k8s.io/cluster-api`, every subpackage of every provider module must build, and the packages that consume `capicore` types must vet. It runs in `verify-parallel` and `verify-ci`; run it locally after **any merge or rebase onto main that touched provider types**, which is where the alias mismatch usually arrives from.

The sync process (`hack/capi-sync-provider.sh`) for each provider:
1. Downloads the upstream module into the Go module cache via `go mod download`
2. Copies types from the cached module into `pkg/capi/<provider>/` using `hack/copy-capi-types/main.go`, which strips functions and types that reference banned imports (e.g. controller-runtime, cloud SDKs)
3. Extracts the CAPI declarations those types need into `pkg/capi/<provider>/capicore/` and repoints the imports at it (`copy-capi-types --localize`), so the module no longer depends on `sigs.k8s.io/cluster-api`
4. Generates deepcopy functions using `controller-gen` inside a temporary Go workspace
5. Runs `go mod tidy` on the provider module, which drops the now-unused CAPI requirement

CRD generation (`hack/capi-workspace-run.sh`) similarly creates a temporary `go.work` on the fly to resolve types across the main module and `pkg/capi/<provider>` modules. No committed workspace file is required.

To add a new CAPI provider or update an existing one:
1. Create or update `hack/capi-vendor/<provider>/go.mod` with the desired upstream version
2. Run `make capi-sync-force` to download the module and regenerate all local copies
3. Run `make verify-capi-sync` to confirm the sync is clean

## Pre-PR Gate

Run proportionate repository-required checks targeting the changes, fix failures, confirm a clean diff/status, and provide command or review evidence.

Required change-specific safety, formatting, linting, generation, and test checks still apply, including the API update requirements above. Select broader checks when the scope or risk demands them; required CI checks remain unchanged. For documentation-only changes, use applicable documentation checks and content review rather than unrelated builds or tests.

Report the actual commands and results or review evidence, and explicitly identify skipped or unavailable checks. Review the complete diff for unintended changes and confirm a clean working tree after committing.

## Go Version

The minimum Go version is declared in [`go.mod`](go.mod). The `api/` module uses `omitzero` struct tags (available since Go 1.24) and other features that require this minimum version.

## Common Gotchas

- **`api/` is a separate Go module**: Always run `make update` after modifying types in the `api/` package. See [Multi-Module Structure](#multi-module-structure) above for details.
- **A clean merge can still break the build**: upstream code that names upstream CAPI types merges without conflict against this branch's localized `capicore` types and fails to compile. Run `make verify-capi-decoupling` after such a merge. See [CAPI Provider Types](#capi-provider-types).
- **Do not modify `vendor/` directories directly**: They are managed by `go mod vendor` via `make update`.
- Select applicable formatting/generation checks as described in [Pre-PR Gate](#pre-pr-gate).
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

Use conventional commit format. The installed `commit-msg` hook validates the pending message automatically; use
`make run-gitlint` to validate a commit range. Do NOT put Jira IDs in commit messages — they belong only in PR titles.

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
2. Complete the proportionate validation and report the evidence described in [Pre-PR Gate](#pre-pr-gate).

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

- Use `sets.Set[T]` (from `k8s.io/apimachinery/pkg/util/sets`) instead of `map[T]struct{}` for set semantics. It provides readable methods (`.Has()`, `.Insert()`, `.Delete()`) and is the standard pattern in the codebase.
- Do not leave dead code (functions defined but never called). Remove unused code before submitting.
- Do not leave TODO comments in validation regex patterns or CEL rules — resolve them before submitting. Reviewers have blocked PRs for shipping regex patterns with placeholder character classes (e.g., allowing `{` and `}` in UUID fields, or missing anchoring constraints).
- When writing regex for API validation, match the upstream format exactly. For UUIDs, use `[0-9a-f]{8}-...`; for Azure resource names, verify the allowed character set against Azure documentation. Do not over-broaden patterns with catch-all classes like `[a-zA-Z0-9-_().{}]`.
