# Testing NodePool drain readiness on a live cluster

This is a manual validation runbook for the advisory NodePool drain-readiness
signal (MCS-1421). It complements [`README.md`](README.md), which documents the
contract. Nothing here gates an upgrade; the signal is status-only.

> Scope: use a **dev or staging** management cluster. This POC pins a
> branch-built control-plane-operator image per hosted cluster, so it is not
> suitable for production as-is.

## What you are validating

The data path, end to end:

```
guest PDB/Pod/Node
  → HCCO nodepooldrain probe
  → HostedControlPlane status (nodePoolDrain[] + nodePoolDrainLastProbeTime + NodePoolDrainUpToDate)
  → HyperShift operator
  → NodePool DrainBlocked condition (True / False / Unknown)
```

Goals: every hop fires, the three states resolve correctly, and no customer
namespace / PDB / Pod names leak to the management cluster.

## Conventions

Commands are tagged by which API they target:

- **[MC]** management cluster kubeconfig
- **[guest]** hosted (guest) cluster kubeconfig

Set these once (adjust to your environment):

```sh
export HC_NS=clusters              # [MC] hosted cluster namespace
export HC=my-trial-cluster         # hosted cluster name
export HCP_NS=${HC_NS}-${HC}       # [MC] hosted control plane namespace
export NP=<nodepool-name>          # a worker NodePool with running nodes
export IMG=quay.io/<you>/hypershift:mcs-1421
```

## 0. Prerequisites

- A hosted cluster with a worker NodePool that has running nodes.
- cluster-admin on both the management and guest clusters.
- A registry reachable by the management cluster's nodes.
- The `hypershift` CLI built from this branch (`make build`).

## 1. Build and push one branch image

HyperShift builds a single image containing both the operator and the
control-plane-operator binaries, so the operator image and the pinned CPO image
are the same artifact.

```sh
cd ~/hypershift
make docker-build docker-push RUNTIME=podman IMG=$IMG
```

## 2. Enable the capability on the management cluster  [MC]

```sh
bin/hypershift install --hypershift-image $IMG
kubectl -n hypershift set env deployment/operator ENABLE_NODEPOOL_DRAIN_READINESS=1
# optional extra platform-namespace exclusions:
# kubectl -n hypershift set env deployment/operator \
#   NODEPOOL_DRAIN_ADDITIONAL_PLATFORM_NAMESPACE_PREFIXES=redhat-,open-cluster-management
kubectl -n hypershift rollout status deployment/operator
```

The env var is the global switch. It registers the HCP→NodePool watch and gates
the condition logic. Without it, nothing happens even if the annotation is set.

## 3. Opt in the trial hosted cluster  [MC]

```sh
kubectl annotate hostedcluster -n $HC_NS $HC \
  hypershift.openshift.io/enable-nodepool-drain-readiness=true \
  hypershift.openshift.io/control-plane-operator-image=$IMG --overwrite
```

## 4. Confirm the plumbing landed  [MC]

```sh
# annotation mirrored HC -> HCP
kubectl -n $HCP_NS get hostedcontrolplane $HC \
  -o jsonpath='{.metadata.annotations.hypershift\.openshift\.io/enable-nodepool-drain-readiness}{"\n"}'

# HCCO has the env var; pod runs the branch image and the nodepooldrain controller
kubectl -n $HCP_NS get deploy hosted-cluster-config-operator \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="ENABLE_NODEPOOL_DRAIN_READINESS")].value}{"\n"}'
kubectl -n $HCP_NS logs deploy/hosted-cluster-config-operator | grep -i nodepooldrain | head
```

## 5. Two-sided inspection helper

Run this after each case to see both ends of the projection:

```sh
# [MC] HCCO's computed result + freshness
kubectl -n $HCP_NS get hostedcontrolplane $HC -o json \
  | jq '{drain: .status.nodePoolDrain, probed: .status.nodePoolDrainLastProbeTime,
         freshness: [.status.conditions[]? | select(.type=="NodePoolDrainUpToDate")]}'

# [MC] the projected NodePool condition
kubectl -n $HC_NS get nodepool $NP -o json \
  | jq '.status.conditions[]? | select(.type=="DrainBlocked")'
```

## 6. Behavior matrix

Apply the manifests below in the **guest** cluster, wait a probe cycle (~1 min),
then inspect with the helper above.

| Case | Manifest / action | Expect |
|---|---|---|
| Blocked | `blocked.yaml` (1 replica, Ready, `minAvailable: 1`) | `DrainBlocked=True` / `BlockedByPodDisruptionBudget`, `blockingCount>=1` |
| Drainable (headroom) | `drainable-headroom.yaml` (2 replicas, `minAvailable: 1`) | `DrainBlocked=False` / `Drainable` |
| AlwaysAllow unready | `alwaysallow-unready.yaml`, then make the pod unready | `DrainBlocked=False` |
| Excluded pods | DaemonSet / mirror / completed / pending pod under a zero-budget PDB | not counted as a blocker |
| Platform exclusion | `platform-ignored.yaml` (zero-budget PDB in `openshift-*`) | ignored; no effect |
| Unknown (stale) | `kubectl -n $HCP_NS scale deploy/hosted-cluster-config-operator --replicas=0`, wait >15 min | `DrainBlocked=Unknown` / `Stale`; `drain_uptodate=0` |
| Unknown (unverified) | fresh enablement before first probe, or opt-in removed | `Unknown` / `Unverified` |
| Unknown (UID mismatch) | delete + recreate a NodePool with the same name | stale entry rejected -> `Unknown` until re-probed |

Remember to scale the HCCO back up after the stale test:

```sh
kubectl -n $HCP_NS scale deploy/hosted-cluster-config-operator --replicas=1   # [MC]
```

### Targeted regression checks for this PR

- **Churn fix (predicate):** with a Blocked PDB in place, rapidly `kubectl edit`
  an *unrelated non-structural* PDB in another guest namespace a few times. The
  signal must **stay True/fresh**, not flap to `Unknown`. Also confirm an idle
  opted-in cluster no longer reconciles its NodePools ~once a minute purely from
  probe-time refreshes (watch operator logs or `controller_runtime_reconcile_total`),
  while a real Blocked<->Drainable transition still propagates within a probe cycle.
- **Unobserved-PDB fix:** editing an unrelated PDB whose reported status is not
  structurally blocking must not blank the scan. Only a *decision-relevant*
  (structurally-blocking) PDB whose status lags its spec should drive `Unknown`
  (`PDBStatusUnobserved`).

## 7. Privacy check (must pass)

```sh
# [MC] names/counts only, never customer workload identifiers
kubectl -n $HCP_NS get hostedcontrolplane $HC -o json | jq '.status.nodePoolDrain'
kubectl -n $HCP_NS logs deploy/hosted-cluster-config-operator | grep -i drain   # reason + count only

# [guest] the DrainBlocked Warning Event is generic (no PDB name)
kubectl get events -A --field-selector reason=DrainBlocked
```

## 8. Metrics

Both gauges are on the HCCO controller-runtime metrics endpoint in `$HCP_NS`:

- `hypershift_nodepool_drain_blocked{namespace,nodepool}` — present=1 only while blocked.
- `hypershift_nodepool_drain_uptodate{namespace}` — 1 fresh, 0 blind (gate failing open).

They are served on `0.0.0.0:8080` and are not scraped by default. Port-forward
the HCCO Deployment (or add a ServiceMonitor) to read them:

```sh
# [MC]
kubectl -n $HCP_NS port-forward deploy/hosted-cluster-config-operator 8080:8080 &
curl -s localhost:8080/metrics | grep hypershift_nodepool_drain
```

## 9. Ground-truth cross-check

Prediction accuracy is the real test. With a **Blocked** pool, trigger an actual
worker roll (a NodePool release or config change) and confirm the drain genuinely
stalls on that PDB. With **Drainable**, confirm the roll completes. Record false
blocks and missed blockers against real eviction outcomes.

## 10. Rollback

```sh
# [MC] per-cluster opt-out: condition removed on next NodePool reconcile
kubectl annotate hostedcluster -n $HC_NS $HC \
  hypershift.openshift.io/enable-nodepool-drain-readiness- \
  hypershift.openshift.io/control-plane-operator-image-

# [MC] fleet-wide disable
kubectl -n hypershift set env deployment/operator ENABLE_NODEPOOL_DRAIN_READINESS-
```

---

## Appendix: sample guest manifests

Apply in the **guest** cluster. `blocked.yaml` is the primary positive case.

### `blocked.yaml` — expect DrainBlocked=True

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: drain-test
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: drain-test
spec:
  replicas: 1
  selector:
    matchLabels: {app: web}
  template:
    metadata:
      labels: {app: web}
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web
  namespace: drain-test
spec:
  minAvailable: 1           # with 1 replica -> desiredHealthy == expectedPods == 1 (structural)
  selector:
    matchLabels: {app: web}
```

### `drainable-headroom.yaml` — expect DrainBlocked=False

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-ha
  namespace: drain-test
spec:
  replicas: 2
  selector:
    matchLabels: {app: web-ha}
  template:
    metadata:
      labels: {app: web-ha}
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web-ha
  namespace: drain-test
spec:
  minAvailable: 1           # with 2 replicas -> desiredHealthy (1) < expectedPods (2): not structural
  selector:
    matchLabels: {app: web-ha}
```

### `alwaysallow-unready.yaml` — expect DrainBlocked=False once the pod is unready

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-aa
  namespace: drain-test
spec:
  replicas: 1
  selector:
    matchLabels: {app: web-aa}
  template:
    metadata:
      labels: {app: web-aa}
    spec:
      containers:
        - name: app
          image: registry.k8s.io/pause:3.9
          readinessProbe:               # flip to failing to make the pod unready
            exec: {command: ["false"]}
            periodSeconds: 5
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: web-aa
  namespace: drain-test
spec:
  minAvailable: 1
  unhealthyPodEvictionPolicy: AlwaysAllow   # unready Running pod is evictable -> not a blocker
  selector:
    matchLabels: {app: web-aa}
```

### `platform-ignored.yaml` — expect no effect (platform namespace excluded)

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-drain-test     # openshift- prefix is excluded by default
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: plat
  namespace: openshift-drain-test
spec:
  replicas: 1
  selector:
    matchLabels: {app: plat}
  template:
    metadata:
      labels: {app: plat}
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: plat
  namespace: openshift-drain-test
spec:
  maxUnavailable: 0            # structurally blocking, but in an excluded namespace
  selector:
    matchLabels: {app: plat}
```

Cleanup:

```sh
# [guest]
kubectl delete namespace drain-test openshift-drain-test
```
