# hypershiftlinter

`hypershiftlinter` is a custom [golangci-lint](https://golangci-lint.run/) plugin
that enforces HyperShift's testing conventions and HC/HCP status-writing rules
through static analysis.

## Why this exists

We already document our testing conventions in
[`TESTING.md`](../../../TESTING.md) and
[`test/e2e/v2/AGENTS.md`](../../../test/e2e/v2/AGENTS.md), but until now nothing
enforced them. Conventions that live only in docs get followed inconsistently —
reviewers have to catch violations by hand, and many slip through.

This plugin turns those conventions into machine-enforced checks instead of
relying on reviewer memory. That matters for several reasons:

1. **Machine-enforced consistency.** Conventions become automated checks rather
   than tribal knowledge. This is what caught real issues in review — for
   example, tests that silently skipped ~60 lines of assertions because guard
   strings no longer matched renamed test cases, and vacuously-passing tests.
2. **Better test quality and reliability.** The `vacuouspass` analyzer catches
   tests that pass without actually asserting anything, a common source of false
   confidence in a test suite.
3. **Cleaner Sippy/CI signal.** Enforcing correct
   `[sig-hypershift][Jira:Hypershift]` and `[Feature:X]` annotations keeps our
   e2e results properly categorized in Sippy.
4. **Lower review burden.** Reviewers spend less time on mechanical naming and
   convention nits and more on substance.

## Analyzers

The plugin ships 10 analyzers, scoped so each rule only fires where it applies.

### Unit test conventions (`TESTING.md`, unit tests only)

| Analyzer       | Enforces                                                                                  |
| -------------- | ----------------------------------------------------------------------------------------- |
| `testcasename` | Test case name fields match `When <condition>, it should <expected behavior>`.            |
| `testfuncname` | Test functions do not use the `Test_` prefix; use `TestFunctionName` instead.             |

### E2E conventions (`test/e2e/v2/` only)

| Analyzer            | Enforces                                                                                          |
| ------------------- | ------------------------------------------------------------------------------------------------ |
| `e2eteststate`      | Requires each v2 Ginkgo subject node to be explicitly labeled `Informing` or `Blocking`.         |
| `guestcluster`      | Bans "guest cluster" terminology; use "hosted cluster" instead.                                  |
| `contextbackground` | Bans `context.Background()` / `context.TODO()` in tests; use `tc.Context` instead.               |
| `vacuouspass`       | Flags vacuously-passing tests that iterate a collection without asserting it is non-empty.        |
| `ipv6url`           | Detects `fmt.Sprintf` URL patterns that break with IPv6; use `net.JoinHostPort` instead.          |
| `sippyannotation`   | Requires the correct Sippy/Jira `[Feature:X]` annotations on Ginkgo `Describe` blocks.            |
| `e2eutilallowlist`  | Restricts `test/e2e/v2` to an allowlist of approved symbols from `test/e2e/util`.                 |

### HostedCluster and HostedControlPlane status patching (repo-wide, see [CNTRLPLANE-3532](https://redhat.atlassian.net/browse/CNTRLPLANE-3532))

| Analyzer          | Enforces                                                                                                    |
| ----------------- | ------------------------------------------------------------------------------------------------------------- |
| `hcpstatuspatch`  | Bans `Status().Update()` and unguarded `MergeFrom()` status patches on `HostedCluster` and `HostedControlPlane`; use `support/statuspatching` instead. Enabled in `.golangci.yml`. |

## How it's built and run

The plugin builds as a Go shared library (`.so`) via
`go build -buildmode=plugin`. The plugin and the golangci-lint host binary must
be compiled from the same `hack/tools/go.mod` — a `golang.org/x/tools` version
mismatch causes `plugin.Open()` to fail at runtime.

Relevant Makefile targets:

- `make hypershiftlinter.so` — build the plugin shared library.
- `make hypershift-lint-all` — run the enabled custom analyzers against the
  current tree.
- `make lint` — run normal lint checks, including these analyzers.
- `make test-linter` — run the analyzers' own unit tests
  (`go test ./hypershiftlinter/analyzers/...`).

Each analyzer has [`analysistest`](https://pkg.go.dev/golang.org/x/tools/go/analysis/analysistest)-based
unit tests with good/bad `testdata/` fixtures.

## Status-writing enforcement and migration exceptions

`hcpstatuspatch` retains its identifier for compatibility and covers both
`HostedCluster` and `HostedControlPlane` from HyperShift's `hypershift/v1beta1`
package, including pointers and type aliases. It flags direct `Status().Update()`
and status patches built with `MergeFrom()` or `MergeFromWithOptions()` without
`MergeFromWithOptimisticLock`. Diagnostics name the actual resource type.

`Update()` conflicts when the resource version is stale. An unguarded merge
patch can silently overwrite concurrent changes. Prefer `support/statuspatching`
to standardize fetching, mutation, no-op detection, and conflict retries;
explicitly optimistic-locked patches are also accepted.

The analyzer ignores `_test.go` files, unrelated types (including namesakes in
other packages), and non-status patches. It follows local patch assignments and
aliases within a function, without interprocedural analysis: patches passed as
function parameters or returned by other functions are outside its scope.
Unguarded merge-patch diagnostics point to the constructor, even when the patch
is later passed to `Status().Patch()` through a variable.

Enforcement is active in normal lint runs. Existing findings have temporary
migration exceptions in `.golangci.yml`; these are migration debt, not approved
status-writing patterns. Each exception combines the `hypershiftlinter` linter,
an anchored exact file path, diagnostic text identifying `hcpstatuspatch`, the
operation and resource type, and an anchored source-line expression with
whitespace tolerance. These filters are combined as described in the
[golangci-lint configuration reference](https://golangci-lint.run/docs/configuration/file/).

Comments name the affected functions and removal condition. Remove an exception
when its last matching call site migrates to `support/statuspatching` (or an
explicitly optimistic-locked patch); `warn-unused: true` helps detect obsolete
rules. An accepted limitation is that an identical new source line in the same
file also matches the exception. A different source line, the same line in
another file, or a diagnostic from another analyzer remains enforced.

[CNTRLPLANE-3532](https://redhat.atlassian.net/browse/CNTRLPLANE-3532) also asks
the linter to warn on `reflect.DeepEqual` for Kubernetes API objects (use
`equality.Semantic.DeepEqual` instead). That check is intentionally a follow-up:
it is a different rule from `hcpstatuspatch`, with a repo-wide blast radius, and
is not registered here.
