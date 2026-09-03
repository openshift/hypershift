# HostedCluster Bring-up: Phase Decomposition

Goal: attribute the wall-clock time from HostedCluster creation to a fully
installed cluster into discrete, controller-owned phases, so regressions can be
pinned to a specific phase.

Method: phase boundaries are the `lastTransitionTime` of specific gate
conditions on the HostedCluster `.status.conditions`. Consecutive deltas between
gate timestamps = phase durations. These gates are stable because each is set by
a distinct controller / reconcile step.

## Critical Path Composition (create -> Available=True)

The first `Available=True` is gated by this sequential dependency chain:

```
create
  -> configValid    (ValidConfiguration=True / ReconciliationSucceeded=True)
  -> infraReady     (InfrastructureReady=True)      [CAPI provisioning]
  -> etcd+KAS       (EtcdAvailable=True -> PublicEndpointExposed=True -> KubeAPIServerAvailable=True)
  -> componentRollout (Available drains ComponentsNotAvailable list)
  -> Available=True
```

Phases:

| Phase | Gate condition(s) | What happens | Typical cost |
|-------|-------------------|--------------|--------------|
| A. Admission & config validation | `ValidReleaseImage`, `SupportedHostedCluster`, `ClusterSizeComputed`, `ValidConfiguration`, `PlatformCredentialsFound`, `ReconciliationSucceeded` | HC accepted; PKI/config validated. Initial `ValidConfiguration=False` on missing PKI secrets is a normal startup race that self-heals. | small (~seconds) |
| B. Infrastructure & external prereqs | `InfrastructureReady=True` (gate); parallel: `ExternalDNSReachable`, `ValidHostedControlPlaneConfiguration`, `ValidReleaseInfo`, `IgnitionEndpointAvailable`, `ValidAzureKMSConfig` | CAPI provisions infra; DNS, ignition, release, KMS/creds settle. Watch for transient cloud-cred errors (e.g. Azure KMS `401` during credential propagation). | DOMINANT #1 (~1.5 min) |
| C. Control-plane core | `EtcdAvailable=True` -> `PublicEndpointExposed=True` -> `KubeAPIServerAvailable=True` | etcd reaches quorum, external endpoint admitted, KAS deployment available. Strictly sequential. | ~1 min |
| D. Control-plane component rollout | `Available` transitions off `ComponentsNotAvailable` | ~30 control-plane components roll out; last blockers are usually `cluster-network-operator` and `ingress-operator`. May blip on `KASLoadBalancerNotReachable`. | DOMINANT #2 (~2 min) |

Key takeaway: Phases B (infra provisioning) and D (component rollout) dominate
time-to-Available. A and C are small, mostly sequential dependencies. Optimize/
watch B and D.

Note: `Available` can briefly flap back to `False` (e.g. `KASLoadBalancerNotReachable`)
shortly after first going `True`. Filter these blips when measuring
time-to-Available (use first sustained `True`).

## Phase E: Guest Cluster Operators / CVO (after Available=True)

`Available=True` does NOT mean the guest cluster is installed — it means the
hosted *control plane* is healthy. Full install continues in the guest cluster:

- Gate conditions: `ClusterVersionReleaseAccepted` -> `ClusterVersionProgressing`
  -> `ClusterVersionSucceeding=True` / `ClusterVersionAvailable=True`.
- The CVO applies the payload and brings up cluster operators. Progress churns
  through `ClusterOperatorsNotAvailable` as operators come online; the last
  operators (commonly `console`, `monitoring`, `image-registry`) gate completion.
- Related data-plane gate: `DataPlaneConnectionAvailable=True` when worker nodes
  join (depends on NodePool provisioning + ignition).

This phase is typically the largest contributor to *total* install time and is
independent of the control-plane Available path above.

## Operationalizing

Compute per-phase durations from the ordered gate timestamps:

```
t_create
t_configValid   = ValidConfiguration=True (or ReconciliationSucceeded=True)
t_infra         = InfrastructureReady=True
t_etcd          = EtcdAvailable=True
t_kas           = KubeAPIServerAvailable=True
t_available     = first sustained Available=True
t_dataplane     = DataPlaneConnectionAvailable=True
t_cvo           = ClusterVersionAvailable=True (or ClusterVersionSucceeding=True)

phaseA = t_configValid - t_create
phaseB = t_infra       - t_configValid
phaseC = t_kas         - t_infra
phaseD = t_available   - t_kas
phaseE = t_cvo         - t_available   (guest operators; overlaps t_dataplane)
```

Attribute any regression to the phase whose delta grew.

## Worked Example (single run)

create 16:13:28 -> Available=True 16:18:03 (~4m35s):

- A (config):     ~4s     (16:13:28 -> 16:13:32)
- B (infra):      ~1m29s  (16:13:32 -> 16:15:01)  <- dominant
- C (etcd+KAS):   ~1m     (16:15:01 -> 16:16:00)
- D (components): ~2m3s   (16:16:00 -> 16:18:03)  <- dominant
- E (CVO/guest):  Available 16:18:03 -> console operator last 16:29:11 (~11m);
                  DataPlaneConnectionAvailable 16:25:42
