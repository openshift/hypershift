# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository. `CLAUDE.md` is a symlink to `AGENTS.md` so that Claude Code auto-loads it; the `AGENTS.md` name is canonical.

HyperShift is middleware for hosting OpenShift control planes at scale, decoupling control planes (running as pods on a management cluster) from worker nodes (running in separate infrastructure).

Detailed guidance lives in the referenced files below; update those files rather than this one.

## Quick Commands

```bash
# Build
make build                        # all binaries
make hypershift-operator          # just the HO

# Test
make test                         # unit tests with race detection
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race -run TestName ./path/to/pkg/...  # single test
make test-envtest-ocp             # CEL validation tests (envtest, k8s 1.31–1.37)

# Code quality
make verify                       # fmt + vet + lint + staticcheck + codespell + gitlint
make lint-fix                     # auto-fix lint violations
make pre-commit                   # full pre-PR gate: build + e2e compile + verify + unit tests

# API / codegen  — REQUIRED after editing anything under api/
make update                       # regenerate CRDs, revendor, sync clients (api-deps→workspace-sync→deps→api→clients→docs-aggregate)
make generate                     # regenerate *_mock.go files (do not hand-edit mocks)
```

**Critical multi-module gotcha**: `api/` is a **separate Go module** (`github.com/openshift/hypershift/api`). Edits there are invisible to the main module until `make update` is run. Always run `make update` after changing any type, constant, or function under `api/`. Never edit `vendor/` directly.

## Commit Format

```
<type>(<scope>): <description>

[optional body]

Signed-off-by: <name> <email>
```

Types: `feat fix docs style refactor test chore build ci perf revert`. Title ≤120 chars, body lines ≤140 chars. No Jira IDs in commit messages — they belong only in PR titles (e.g. `OCPBUGS-12345: …`).

For architecture, components, and platform support, see [ARCHITECTURE.md](ARCHITECTURE.md).

Project documentation is published via MkDocs. The site structure and navigation are defined in [docs/mkdocs.yml](docs/mkdocs.yml), with content under `docs/content/`. When adding or reorganizing documentation pages, update the `nav` section in `mkdocs.yml` to keep the site navigation in sync.

## Key References

| Topic | Where to look |
|-------|---------------|
| **Development commands and patterns** | [DEVELOPMENT.md](DEVELOPMENT.md) |
| **API types and CRD machinery** | [api/AGENTS.md](api/AGENTS.md) |
| **Control plane components (CPOv2)** | [support/controlplane-component/AGENTS.md](support/controlplane-component/AGENTS.md) and [support/controlplane-component/README.md](support/controlplane-component/README.md) |
| **Control plane operator** | [control-plane-operator/AGENTS.md](control-plane-operator/AGENTS.md) |
| **HC/HCP status patching** | [control-plane-operator/AGENTS.md](control-plane-operator/AGENTS.md#hostedcluster-and-hostedcontrolplane-status-patching) — never `Status().Update()`/unguarded `MergeFrom()` on HC/HCP; actively enforced by `hcpstatuspatch`; use `support/statuspatching` |
| **E2E v2 test framework** | [test/e2e/v2/AGENTS.md](test/e2e/v2/AGENTS.md) |
| **Envtest (CEL validation tests)** | [test/envtest/README.md](test/envtest/README.md) — YAML-driven, runs across k8s 1.31–1.37, supports feature gate filtering |
| **CEL over webhooks** | [.claude/rules/webhook-validation.md](.claude/rules/webhook-validation.md) |
| **Restructuring commits** | [skills/restructure-commits/SKILL.md](skills/restructure-commits/SKILL.md) — component-based commit layout; verify with `make verify-commit-layout` |
| **Code formatting** | [DEVELOPMENT.md](DEVELOPMENT.md) — code quality commands and conventions |
| **Unit test conventions** | [TESTING.md](TESTING.md) — naming, placement, and creation requirements |
| **Design invariants** | [docs/content/reference/goals-and-design-invariants.md](docs/content/reference/goals-and-design-invariants.md) |
| **Versioning and support** | [docs/content/reference/versioning-support.md](docs/content/reference/versioning-support.md) |
| **Upgrades lifecycle** | [docs/content/how-to/upgrades.md](docs/content/how-to/upgrades.md) |
| **Contributing and PR workflow** | [CONTRIBUTING.md](CONTRIBUTING.md) |
| **Agentic SDLC framework** | [docs/content/how-to/agentic-sdlc.md](docs/content/how-to/agentic-sdlc.md) |
| **Feature gates** | [docs/content/how-to/feature-gates.md](docs/content/how-to/feature-gates.md) — two separate systems: management cluster gates vs hosted cluster gates |
| **Pre-commit hooks** | [docs/content/contribute/precommit-hook-help.md](docs/content/contribute/precommit-hook-help.md) |

## Pull Secret Cycling

When changing how workers and the hosted cluster authenticate to registries, treat **HostedCluster** `spec.pullSecret`, **management-cluster** Secret data, **HCCO** reconciliation into the data plane, and optional **Global Pull Secret** (`kube-system/additional-pull-secret`) as one system. Changing only the Secret bytes in place does not change `spec.pullSecret` and therefore does not drive a **NodePool** rollout, but controllers can still propagate credentials into the control plane namespace, guest `openshift-config`, `kube-system/original-pull-secret`, and (where the Global Pull Secret DaemonSet is scheduled) kubelet configuration.

See [docs/content/how-to/common/global-pull-secret.md](docs/content/how-to/common/global-pull-secret.md) for behavior, platform and NodePool eligibility (AWS/Azure Replace vs InPlace and other platforms), merge semantics, and operational guidance.

## Fleet-Wide Rollout Impact

Changes to config data, secrets, or any value that feeds into a NodePool config hash will trigger a rollout across **all** HostedClusters. Before adding new data to ignition configs, MachineConfigs, or any resource reconciled into the data plane, check whether the change affects the NodePool config hash (search for `hashStruct` / `configHash`). If it does, the PR **must** pass `e2e-aws-upgrade-hypershift-operator` to prove the rollout is safe.

Additionally, consider the **deployment-time migration impact** on existing NodePools: if the hash was previously computed with different inputs (e.g., an image resolved from a different source), deploying the change will cause a one-time hash change and rollout across all affected NodePools — even when HC and NodePool are at the same version. Reviewers must ask: "what happens to clusters that already exist when this operator version is deployed?"
