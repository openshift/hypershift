# P9: Guest convergence and completion

Use the guest kubeconfig supplied for this HC. If obtaining it is necessary,
resolve the Secret from HC `status.kubeconfig.name`, verify its HC ownership
and creation identity, and save it locally with restrictive permissions. Do
not assume a predecessor's kubeconfig still targets the new HC with the same
name. Do not print Secret data or include the kubeconfig in an evidence bundle.

```bash
set -euo pipefail
umask 077
oc --kubeconfig "$GUEST_KUBECONFIG" get clusterversion version -o json \
  > "$RESTRICTED_DIR/clusterversion.raw.json" \
  2> "$RESTRICTED_DIR/clusterversion.raw.stderr"
jq '.metadata.generation as $generation |
  .status.observedGeneration as $observedGeneration | {
  generation: $generation,
  observedGeneration: $observedGeneration,
  fresh: ($generation != null and $observedGeneration != null and
    $observedGeneration == $generation),
  desiredVersion: .status.desired.version,
  history: [.status.history[:2][]? |
    {version, state, startedTime, completionTime}],
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]}' \
  "$RESTRICTED_DIR/clusterversion.raw.json"

oc --kubeconfig "$GUEST_KUBECONFIG" get clusteroperators -o json \
  > "$RESTRICTED_DIR/clusteroperators.raw.json" \
  2> "$RESTRICTED_DIR/clusteroperators.raw.stderr"
jq '[.items[] as $operator |
  ["Available", "Progressing", "Degraded"][] as $type |
  ($operator.status.conditions // [] |
    map(select(.type == $type)) | first) as $condition |
  select($condition == null or
    ($type == "Available" and $condition.status != "True") or
    ($type != "Available" and $condition.status != "False")) |
  {name: $operator.metadata.name, condition: $type,
   status: ($condition.status // "Missing"),
   lastTransitionTime: ($condition.lastTransitionTime // null),
   observedGeneration: ($condition.observedGeneration // null)}]' \
  "$RESTRICTED_DIR/clusteroperators.raw.json"
jq '[.items[] | {name: .metadata.name,
  conditions: [.status.conditions[]? |
    {type, status, reason, message, lastTransitionTime, observedGeneration}]}]' \
  "$RESTRICTED_DIR/clusteroperators.raw.json" \
  > "$RESTRICTED_DIR/clusteroperator-condition-details.raw.json"
```

The allowlisted view reports missing and `Unknown` required conditions as
unresolved. Use the restricted condition details to investigate a specific
operator; messages and raw command output can contain sensitive hostnames or
other values. Redact before printing or retaining details in ordinary
evidence.

For a remaining operator, inspect its conditions, operand custom resources,
deployments, events and controller logs. Some guest operators have controllers
running in the HCP namespace; use the actual deployment location.

For monitoring specifically, inspect `Prometheus/k8s` and CMO reconciliation
tasks, not only existing pods:

```bash
set -euo pipefail
umask 077
oc --kubeconfig "$GUEST_KUBECONFIG" -n openshift-monitoring \
  get prometheus,deployments,statefulsets,pods -o json \
  > "$RESTRICTED_DIR/monitoring-resources.raw.json" \
  2> "$RESTRICTED_DIR/monitoring-resources.raw.stderr"
jq '[.items[] | {kind, name: .metadata.name,
  phase: .status.phase,
  replicas: .status.replicas,
  readyReplicas: .status.readyReplicas,
  availableReplicas: .status.availableReplicas,
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]}]' \
  "$RESTRICTED_DIR/monitoring-resources.raw.json"
oc --kubeconfig "$GUEST_KUBECONFIG" -n openshift-monitoring \
  logs deployment/cluster-monitoring-operator --since=10m --tail=250 \
  > "$RESTRICTED_DIR/cluster-monitoring-operator.raw.log" 2>&1
```

A dependency error may be followed by an availability-validation timer before
a retry. Use the exact error and observed progress; do not infer expired STS
credentials from an absent Prometheus. The observations reference describes
the pinned release where this was measured; it is not a universal timer.

HCCO copies version status and CVO conditions from the guest ClusterVersion
into HCP status, and HO then copies them into HC status. Inspect guest
ClusterOperator resources for individual operator health. A brief lag between
guest completion and HC history completion can be reconciliation delay. If the
guest is complete but HC stays stale, inspect HCCO/HO status propagation.

HC `status.version.observedGeneration` comes from the guest ClusterVersion.
Compare the guest ClusterVersion's `status.observedGeneration` with its own
`metadata.generation`. HC and HCP condition generations refer to their own
objects and must be checked separately.

For a full-cluster target, verify these together in the current observation:

- Required HC validation passes with no blocking required condition false.
  HC and HCP `Available=True` conditions must have
  `observedGeneration == metadata.generation`; otherwise availability is
  unverified.
- Relevant CPCs are available and rollout-complete at the desired version.
  Resolve the requested control-plane image from HC
  `spec.controlPlaneRelease.image`, falling back to `spec.release.image`, and
  require HCP `status.controlPlaneVersion` to be current for the HCP generation.
  Its latest history entry must be `Completed` with image and semantic version
  matching the resolved target and `status.controlPlaneVersion.desired`.
- Every requested NodePool has fresh `Ready`, `AllMachinesReady` and
  `AllNodesHealthy` true conditions. Resolve `spec.release.image` to its
  semantic version and compare it with `status.version`; require no active
  `UpdatingVersion` or `UpdatingConfig` condition. For fixed-size pools compare
  ready workers with `spec.replicas`; for autoscaled pools report the min/max
  bounds and observed replicas as a dynamic target, not as `spec.replicas=null`.
  Respect intentional zero replicas or control-plane-only scope.
- Guest ClusterVersion's `status.observedGeneration` matches its own
  `metadata.generation`, and it is Available, not Progressing and not Failing.
  Its latest history entry is Completed for the intended release. Missing or
  stale generation evidence leaves guest convergence unverified. Resolve
  tag/digest equivalence from release metadata rather than comparing unrelated
  strings.
- Every expected guest ClusterOperator is Available, not Progressing and not
  Degraded. Treat missing or unknown required conditions as unresolved; do not
  accept an empty or partial operator list as convergence.
- Resolve the data-plane target from HC `spec.release.image`; its latest
  `status.version.history[0]` entry must be `Completed`, with image and semantic
  version matching both that resolved target and `status.version.desired`.
  Resolve tag/digest equivalence through release metadata. Missing or stale
  release evidence leaves P9 unverified.

Management `Progressing=False` alone does not establish these conditions. If
guest checks cannot be performed, label full guest convergence unverified. For
an explicit control-plane-only target, report the applicable control-plane
gates and state which worker/guest workload checks are outside that target.
