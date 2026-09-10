# hypershiftlinter

`hypershiftlinter` is a custom [golangci-lint](https://golangci-lint.run/) plugin
that automatically enforces HyperShift's testing conventions through static
analysis.

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

The plugin ships 9 analyzers, scoped so each rule only fires where it applies.

### Unit test conventions (`TESTING.md`, unit tests only)

| Analyzer       | Enforces                                                                                  |
| -------------- | ----------------------------------------------------------------------------------------- |
| `testcasename` | Test case name fields match `When <condition>, it should <expected behavior>`.            |
| `testfuncname` | Test functions do not use the `Test_` prefix; use `TestFunctionName` instead.             |

### E2E conventions (`test/e2e/v2/` only)

| Analyzer            | Enforces                                                                                          |
| ------------------- | ------------------------------------------------------------------------------------------------ |
| `guestcluster`      | Bans "guest cluster" terminology; use "hosted cluster" instead.                                  |
| `contextbackground` | Bans `context.Background()` / `context.TODO()` in tests; use `tc.Context` instead.               |
| `vacuouspass`       | Flags vacuously-passing tests that iterate a collection without asserting it is non-empty.        |
| `ipv6url`           | Detects `fmt.Sprintf` URL patterns that break with IPv6; use `net.JoinHostPort` instead.          |
| `sippyannotation`   | Requires the correct Sippy/Jira `[Feature:X]` annotations on Ginkgo `Describe` blocks.            |
| `e2eutilallowlist`  | Restricts `test/e2e/v2` to an allowlist of approved symbols from `test/e2e/util`.                 |

### HostedControlPlane status patching (repo-wide, see [CNTRLPLANE-3532](https://redhat.atlassian.net/browse/CNTRLPLANE-3532))

| Analyzer          | Enforces                                                                                                    |
| ----------------- | ------------------------------------------------------------------------------------------------------------- |
| `hcpstatuspatch`  | Bans `Status().Update()` and unguarded `MergeFrom()` status patches on `HostedControlPlane`; use `support/statuspatching` instead. **Not yet enabled in `.golangci.yml`** — see below. |

## How it's built and run

The plugin builds as a Go shared library (`.so`) via
`go build -buildmode=plugin`. The plugin and the golangci-lint host binary must
be compiled from the same `hack/tools/go.mod` — a `golang.org/x/tools` version
mismatch causes `plugin.Open()` to fail at runtime.

Relevant Makefile targets:

- `make hypershiftlinter.so` — build the plugin shared library.
- `make hypershift-lint-all` — opt-in target to run the analyzers against the
  current tree.
- `make test-linter` — run the analyzers' own unit tests
  (`go test ./hypershiftlinter/analyzers/...`).

Each analyzer has [`analysistest`](https://pkg.go.dev/golang.org/x/tools/go/analysis/analysistest)-based
unit tests with good/bad `testdata/` fixtures.

## Staged rollout

Enablement is intentionally staged. The initial change lands the plugin, the
analyzers, and their unit tests only — **it does not enable enforcement**. A
follow-up wires the plugin into `.golangci.yml` / `make lint` and fixes the
existing violations in the tree.

Splitting it this way keeps the review surface small and lets CI actually run the
analyzers' own tests before enforcement is turned on. (A brand-new reusable
workflow can't get a green pre-merge run on the PR that introduces it, because
GitHub resolves `uses: ...@main` and the `pull_request` trigger from the base
branch — so the foundational plumbing has to land on `main` first.)

The same staging applies per-analyzer when a new rule would fail on violations
still being fixed in other in-flight PRs: `hcpstatuspatch` lands with its own
tests but is deliberately left out of `linters.settings.custom.hypershiftlinter.settings.analyzers.enable`
in `.golangci.yml` until the last HostedControlPlane status-patch call sites it
would flag are migrated to `support/statuspatching` elsewhere.

[CNTRLPLANE-3532](https://redhat.atlassian.net/browse/CNTRLPLANE-3532) also asks
the linter to warn on `reflect.DeepEqual` for Kubernetes API objects (use
`equality.Semantic.DeepEqual` instead). That check is intentionally a follow-up:
it is a different rule from `hcpstatuspatch`, with a repo-wide blast radius, and
is not registered here.
