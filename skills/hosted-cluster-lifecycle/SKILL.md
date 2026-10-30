---
name: hosted-cluster-lifecycle
description: Guide HyperShift HostedCluster setup and diagnose incomplete startup on AWS or self-managed Azure. Identify readiness gates, inspect the next blocking dependency, and capture startup or teardown evidence.
---

# Hosted Cluster Lifecycle

Help the user reach their requested readiness target, or explain precisely
what is preventing it. Load detail progressively: read intake, choose the
relevant workstream, and open only the references needed for the current gate.
Do not load every reference or the entire roadmap at the start.

## Start here

1. Read [intake](references/intake.md). Resolve the task mode, target management
   cluster, HC identity, provider, and requested readiness target. For supplied
   captures, work offline unless live inspection is also requested. For live
   work, run intake in the same Bash session as later management commands; its
   `mgmt_oc` helper applies the explicit kubeconfig and optional context.
2. For a new cluster, read [setup](references/setup.md) and the selected
   provider reference. For an existing cluster, take the small intake snapshot
   and use the routing table below. If deletion has started, use teardown.
3. Name the earliest unresolved dependency in each relevant workstream. Load
   that workstream's reference and collect its smallest useful evidence set.
4. Re-evaluate after new evidence or an authorized change. Track current
   readiness separately from historical first-success timestamps.

## Load detail when needed

| Evidence or task | Read next |
|------------------|-----------|
| Plan or create a cluster; no HC yet | [Setup](references/setup.md) |
| P0: validation blocked; P1: HCP/operator scaffold missing | [Control plane](references/control-plane.md), P0–P1 |
| P2: endpoints unpublished; P3: foundation unready; P4: KAS unavailable | [Control plane](references/control-plane.md), P2–P4 |
| P5: HCP unavailable, CPC dependencies or rollout incomplete | [Control plane](references/control-plane.md), P5 |
| P6: NodePool exists but bootstrap or machine workload missing | [Workers](references/workers.md), P6 |
| P7: Machines exist, provisioning or ignition pending | [Workers](references/workers.md), P7 |
| P8: Nodes absent/unhealthy or NodePool not ready | [Workers](references/workers.md), P8 |
| P9: workers ready, guest operators/version still incomplete | [Guest convergence](references/guest.md) |
| AWS credentials, default worker SG, CAPA, endpoints, or cloud resources | [AWS](references/aws.md) |
| Azure identities, CAPZ, private connectivity, or VM/network resources | [Azure](references/azure.md) |
| Monitor startup, retain timings, or compare observations | [Evidence capture](references/evidence.md) |
| Interpret the two observed AWS HostedClusters or a five-minute wait | [Observed AWS cases](references/observations.md) |
| HC has a deletion timestamp or teardown is being investigated | [Teardown](references/teardown.md) |

P0–P9 are dependency labels, not a single serial state machine. Control-plane,
worker, and guest work overlap. A late validation failure can block a cluster
whose API server already exists. Report parallel waits without inventing a
single percentage-complete value.

## Operational boundaries

- Follow the user's selected kubeconfig/context, scripts, images, topology,
  and existing authorization. A setup request authorizes its intended setup;
  a debugging request does not itself authorize replacement or destruction.
  Do not add another confirmation when the necessary action is already authorized.
- Use explicit management and guest kubeconfigs and their selected contexts.
  Resolve ambiguous targets
  before accessing or changing them; never infer the target from the default
  context. Namespace alone is not a unique cluster identity: retain the HC UID.
- Diagnose from conditions, dependency messages, events, and version-matched
  controller code. Query failures and missing evidence remain unknown.
  `Unknown` is not proof of invalid credentials, and a long wait alone is not
  proof of a stuck cluster.
- Preserve credentials locally with restricted permissions. Do not print
  Secret data, kubeconfigs, or token-bearing bootstrap content into responses
  or ordinary evidence bundles.
- Preserve normal controller cleanup. Finalizer removal, operator reinstallation,
  broad IAM/network changes, and repeated cluster recreation require their own
  concrete justification and authorization; they are not diagnostic defaults.
- The measured AWS cases are version-specific observations. Do not apply their
  images, elapsed times, timer behavior, or repair history as setup defaults.

## Report a useful result

State the target and mode; current control-plane, worker, and guest gates;
the blocking condition/reason and its object; the next narrow inspection or
authorized action; what remains unverified; and where evidence was saved.
For progress updates, distinguish pending with observed progress, a concrete
failure, and insufficient evidence. Do not call a cluster fully ready from
`Available=True`, running pods, or `Progressing=False` alone. Use the completion
criteria in the guest reference for a full-cluster target, or explicitly name
the narrower control-plane-only target.
