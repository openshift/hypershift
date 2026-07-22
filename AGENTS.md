# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository. `CLAUDE.md` is a symlink to this file so that Claude Code auto-loads it; the `AGENTS.md` name is canonical.

HyperShift is middleware for hosting OpenShift control planes at scale, decoupling control planes (running as pods on a management cluster) from worker nodes (running in separate infrastructure).

This file is intentionally minimal — detailed guidance lives in the referenced files below and should be updated there, not here.

For architecture, components, and platform support, see [ARCHITECTURE.md](ARCHITECTURE.md).

Project documentation is published via MkDocs. The site structure and navigation are defined in [docs/mkdocs.yml](docs/mkdocs.yml), with content under `docs/content/`. When adding or reorganizing documentation pages, update the `nav` section in `mkdocs.yml` to keep the site navigation in sync.

## Key References

| Topic | Where to look |
|-------|---------------|
| **Development commands and patterns** | [DEVELOPMENT.md](DEVELOPMENT.md) |
| **API types and CRD machinery** | [api/AGENTS.md](api/AGENTS.md) |
| **Control plane components (CPOv2)** | [support/controlplane-component/AGENTS.md](support/controlplane-component/AGENTS.md) and [support/controlplane-component/README.md](support/controlplane-component/README.md) |
| **Control plane operator** | [control-plane-operator/AGENTS.md](control-plane-operator/AGENTS.md) |
| **E2E v2 test framework** | [test/e2e/v2/AGENTS.md](test/e2e/v2/AGENTS.md) |
| **E2E async assertions** | [test/e2e/util/AGENTS.md](test/e2e/util/AGENTS.md) — `EventuallyObject`/`EventuallyObjects` required for all Kubernetes object polling |
| **Envtest (CEL validation tests)** | [test/envtest/README.md](test/envtest/README.md) — YAML-driven, runs across k8s 1.30–1.35, supports feature gate filtering |
| **CEL over webhooks** | [.claude/rules/webhook-validation.md](.claude/rules/webhook-validation.md) |
| **Code formatting** | [DEVELOPMENT.md](DEVELOPMENT.md) — code quality commands and conventions |
| **Unit test conventions** | [TESTING.md](TESTING.md) — naming, placement, and creation requirements |
| **Design invariants** | [docs/content/reference/goals-and-design-invariants.md](docs/content/reference/goals-and-design-invariants.md) |
| **Versioning and support** | [docs/content/reference/versioning-support.md](docs/content/reference/versioning-support.md) |
| **Upgrades lifecycle** | [docs/content/how-to/upgrades.md](docs/content/how-to/upgrades.md) |
| **Contributing and PR workflow** | [CONTRIBUTING.md](CONTRIBUTING.md) |
| **Agentic SDLC framework** | [docs/content/how-to/agentic-sdlc.md](docs/content/how-to/agentic-sdlc.md) |
| **Pre-commit hooks** | [docs/content/contribute/precommit-hook-help.md](docs/content/contribute/precommit-hook-help.md) |

## Pull Secret Cycling

When changing how workers and the hosted cluster authenticate to registries, treat **HostedCluster** `spec.pullSecret`, **management-cluster** Secret data, **HCCO** reconciliation into the data plane, and optional **Global Pull Secret** (`kube-system/additional-pull-secret`) as one system. Changing only the Secret bytes in place does not change `spec.pullSecret` and therefore does not drive a **NodePool** rollout, but controllers can still propagate credentials into the control plane namespace, guest `openshift-config`, `kube-system/original-pull-secret`, and (where the Global Pull Secret DaemonSet is scheduled) kubelet configuration.

See [docs/content/how-to/common/global-pull-secret.md](docs/content/how-to/common/global-pull-secret.md) for behavior, platform and NodePool eligibility (AWS/Azure Replace vs InPlace and other platforms), merge semantics, and operational guidance.

## Fleet-Wide Rollout Impact

Changes to config data, secrets, or any value that feeds into a NodePool config hash will trigger a rollout across **all** HostedClusters. Before adding new data to ignition configs, MachineConfigs, or any resource reconciled into the data plane, check whether the change affects the NodePool config hash (search for `hashStruct` / `configHash`). If it does, the PR **must** pass `e2e-aws-upgrade-hypershift-operator` to prove the rollout is safe.
