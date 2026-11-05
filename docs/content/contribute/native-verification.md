# Native verification

Run the complete verification pipeline locally:

```sh
make verify
```

The command requires Go, Git, and Make. To run without Make:

```sh
GOOS= GOARCH= CGO_ENABLED=0 GOWORK=off GOFLAGS=-mod=vendor go -C hack/tools run ./cmd/verify
```

The Go driver in `hack/tools/verify` runs every tool with `CGO_ENABLED=0`, the
host OS and architecture, and the exact Go version declared in the root
`go.mod`. Go downloads that toolchain when necessary. Tool dependencies are
pinned in `hack/tools/go.mod` and built from its vendor tree. No container
runtime, C compiler, Python, Bash, jq, curl, or system YAML utility is needed
by this pipeline. The optional pre-commit framework and commit-restructuring
tools retain their own installation requirements.

## What runs

Preparation runs in order: API and workspace dependencies, localized CAPI
provider types, root and tool dependencies, CRDs and API documentation, clients,
aggregated documentation, ownership rules, mock generation, and formatting.
Each preparation task runs once per invocation. Generated output is checked
for uncommitted changes at the end.

After preparation, verification checks CAPI decoupling, staticcheck, vet, both
modules' golangci-lint configurations, API dependency policy, CRD compatibility,
spelling, commit messages, Codecov configuration, CPO container-file agreement,
docs navigation, and paired Tekton pipelines. No configured linter is removed.
The custom HyperShift and Kubernetes API linters are compiled into golangci-lint
using its module plugin interface; Go shared-library plugins are not used.

Read-only checks run with two workers by default. Set `VERIFY_JOBS=1` to reduce
peak resource use, or a higher positive value when resources permit. Generation
finishes before these checks begin. Go's build and module caches are reused
between runs; no separate verification image needs building or publishing.

## Local and CI behavior

`verify-ci` and `verify-quick` are compatibility aliases for the complete
`verify` pipeline. Individual Make targets such as `lint`, `generate`, `update`,
and `verify-docs-nav` remain available while using the same Go implementations.
GitHub Actions runs `make verify-repository` and `make lint` in separate jobs
on Linux and macOS. The companion Prow configuration runs the same two commands
in independent checkouts. Their combined coverage matches local `make verify`;
both jobs must pass. The lint job includes standalone staticcheck and vet as
well as both golangci-lint configurations.

Separate jobs can run concurrently without generation racing with analysis.
Each job has its own preparation and caches; job startup and duplicate
preparation can offset some of the elapsed-time benefit. Do not run independent
verification and lint processes concurrently in one checkout.

Set `PULL_BASE_SHA` to select the comparison revision for API lint, CRD schema
compatibility, and commit messages. By default, the driver uses the local
`main` tracking reference for the upstream HyperShift remote, falling back to
`main`. Set `PULL_PULL_SHA` to select the PR head for commit-message checks; it
defaults to `HEAD`. CI should provide both revisions and fetch their history.

Module downloads and Codecov's configuration-validation service require network
access. A failed external validation fails the run. Native Go removes the
Python and shell toolchain dependencies; the host operating system still
provides process execution, filesystem semantics, and networking.

## Updating tools and policies

Update Go tools in `hack/tools/go.mod`, then run `make update` and commit module
and vendor changes. The root `go.mod` selects the verification toolchain. Keep
the tools module's preferred `toolchain` directive aligned when changing it.
The two golangci-lint configurations remain the sources for enabled linters,
settings, and exclusions.

Spelling uses codespell 2.4.1's clear and rare dictionaries, including the
existing `.codespellignore` and path exclusions. Dictionary source, attribution,
and update instructions live in `hack/tools/verify/spelling/README.md`. Commit
policy remains in `.gitlint`; unsupported future settings fail explicitly.
Ownership policy lives in `hack/tools/verify/ownership.go`.

Run `make test-linter` to test the driver and both native linter integrations.
The tests cover rejected input as well as passing input, including Tekton drift,
nav ordering, spelling ignores, conventional commits, and ownership grouping.
