# P0–P5: Find the blocking control-plane dependency

Run intake first in the same Bash session. These examples use its `mgmt_oc`
helper, which supplies the explicit management kubeconfig and optional
`MGMT_CONTEXT` to every management-cluster command. If you start a new shell,
define that helper from intake before running a block. Read only the subsection
that matches the current gate.

Condition reasons, messages and logs can contain sensitive values. Keep their
restricted projections and raw command output under `RESTRICTED_DIR`; print
only the allowlisted status view. Redact before printing or retaining details
in ordinary evidence, as described in the evidence reference.

## P0: Intake or validation

If HC is absent, use setup's pre-HC failure path. If present, inspect its
required validation conditions and `Progressing` reason. Common condition
families concern configuration, release image, platform identity, KMS, proxy
and IDP inputs; their presence and ownership vary by version/provider. An
identity/OIDC failure can arise after HCP and KAS exist, so inspect current
validation even when resources are already running.

```bash
set -euo pipefail
umask 077
if ! mgmt_oc -n "$HC_NAMESPACE" get hostedcluster "$HC_NAME" -o json \
  2> "$RESTRICTED_DIR/hc-validation-query.raw.stderr" |
  jq '{metadata: {generation: .metadata.generation},
      status: {conditions: [.status.conditions[]? |
        {type, status, reason, message, lastTransitionTime, observedGeneration}]}}' \
      > "$RESTRICTED_DIR/hc-validation.raw.json" \
      2> "$RESTRICTED_DIR/hc-validation-format.raw.stderr"; then
  echo "HostedCluster condition query failed; inspect restricted stderr." >&2
  exit 1
fi
if ! jq '
  .metadata.generation as $generation |
  {generation: $generation,
   conditions: [.status.conditions[]? | {
     type, status, lastTransitionTime, observedGeneration,
     fresh: (.observedGeneration == $generation)
   }]}' "$RESTRICTED_DIR/hc-validation.raw.json" \
  > "$RESTRICTED_DIR/hc-validation.allowlisted.json" \
  2> "$RESTRICTED_DIR/hc-validation-format.raw.stderr"; then
  echo "Could not format HostedCluster conditions." >&2
  exit 1
fi
jq '.status.conditions' "$RESTRICTED_DIR/hc-validation.raw.json" \
  > "$RESTRICTED_DIR/hc-validation-details.raw.json"
cat "$RESTRICTED_DIR/hc-validation.allowlisted.json"
mgmt_oc -n "$HO_NAMESPACE" logs deployment/"$HO_DEPLOYMENT" \
  --since=10m --tail=200 \
  > "$RESTRICTED_DIR/hypershift-operator.raw.log" 2>&1
```

Inspect the restricted condition details only as needed to confirm the failed
input. Confirm that input before proposing credential rotation or an HO
rollout. Provider identity details are in the AWS/Azure references.

## P1: Scaffold

Look for HCP, platform infrastructure CR, CAPI Cluster and management operator
deployments. Their creation is different from readiness. The expected core
chain is HCP → provider infrastructure object → CAPI Cluster; operator setup
and other reconciliation then overlap.

```bash
set -euo pipefail
mgmt_oc -n "$HCP_NAMESPACE" get clusters.cluster.x-k8s.io,deployments,pods
mgmt_oc -n "$HCP_NAMESPACE" get events --sort-by=.lastTimestamp -o json \
  > "$RESTRICTED_DIR/control-plane-events.raw.json" \
  2> "$RESTRICTED_DIR/control-plane-events.raw.stderr"
jq '[.items[] | {type, reason, firstTimestamp, lastTimestamp, count,
  objectKind: .involvedObject.kind,
  objectNamePresent: (.involvedObject.name != null)}]' \
  "$RESTRICTED_DIR/control-plane-events.raw.json"
```

For a missing object, capture HO logs in the restricted directory and inspect
the last created dependency. For a present but unready deployment, inspect its
pods/init containers and events. An AWS provider pod may wait for
`service-network-admin-kubeconfig` while KAS starts; an early `FailedMount`
alone does not prove bad cloud credentials.

## P2: Published endpoints

HCP `InfrastructureReady=True` means required endpoint addresses were
published. It does not certify the provider infrastructure object, worker
network, DNS reachability from every client, or worker readiness.

```bash
set -euo pipefail
mgmt_oc -n "$HCP_NAMESPACE" get hostedcontrolplane "$HC_NAME" -o json \
  > "$RESTRICTED_DIR/hcp-endpoints.raw.json" \
  2> "$RESTRICTED_DIR/hcp-endpoints.raw.stderr"
jq '[.status.conditions[]? | {type, status, lastTransitionTime,
  observedGeneration}]' "$RESTRICTED_DIR/hcp-endpoints.raw.json"
mgmt_oc -n "$HCP_NAMESPACE" get services -o json \
  > "$RESTRICTED_DIR/hcp-services.raw.json" \
  2> "$RESTRICTED_DIR/hcp-services.raw.stderr"
jq '[.items[] | {name: .metadata.name, type: .spec.type,
  loadBalancerPublished: ([.status.loadBalancer.ingress[]?] | length > 0)}]' \
  "$RESTRICTED_DIR/hcp-services.raw.json"
```

Inspect Routes only where the API and publishing strategy use them. Follow the
condition to the missing Service/LB address, Route, DNS or private endpoint.
For cloud/private networking details, load the selected provider reference.

## P3–P4: Foundation and KAS

With managed etcd, start at etcd and featuregate-generator dependencies before
KAS. Include configured etcd shard CPCs such as `etcd-events`; both the default
etcd component and shards are exempt from the implicit KAS dependency.
With external etcd, follow its configured endpoint/identity path instead
of diagnosing a deliberately absent managed etcd StatefulSet. Listing the CPC
collection and filtering it tolerates components that have not been created
yet.

```bash
set -euo pipefail
mgmt_oc -n "$HCP_NAMESPACE" get controlplanecomponents -o json \
  > "$RESTRICTED_DIR/controlplanecomponents.raw.json" \
  2> "$RESTRICTED_DIR/controlplanecomponents.raw.stderr"
jq '[.items[] | select(.metadata.name == "etcd" or
    (.metadata.name | startswith("etcd-")) or
    .metadata.name == "featuregate-generator" or
    .metadata.name == "kube-apiserver") | {
  name: .metadata.name,
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]
}]' "$RESTRICTED_DIR/controlplanecomponents.raw.json"
mgmt_oc -n "$HCP_NAMESPACE" get pods,pvc,jobs
mgmt_oc -n "$HCP_NAMESPACE" get deployment kube-apiserver -o json \
  > "$RESTRICTED_DIR/kube-apiserver.raw.json" \
  2> "$RESTRICTED_DIR/kube-apiserver.raw.stderr"
jq '{name: .metadata.name, generation: .metadata.generation,
  readyReplicas: .status.readyReplicas,
  availableReplicas: .status.availableReplicas,
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]}' \
  "$RESTRICTED_DIR/kube-apiserver.raw.json"
```

For managed etcd, also inspect the default and configured shard CPCs, PVC binding,
storage/scheduling events and pod logs. For the featuregate generator, inspect
the Job and its pod logs. For KAS, distinguish Deployment availability
(`KubeAPIServerAvailable`) from the separate endpoint health check used for
HCP availability. `KASLoadBalancerNotReachable` can indicate DNS or network
reachability even with a healthy KAS deployment.

For public `Route` or `LoadBalancer` publishing, CPO probes the corresponding
endpoint. For a private HCP, it probes the internal `kube-apiserver` Service.
A healthy internal Service can leave HCP `Available=True` while the published
private endpoint is unreachable. Test that endpoint independently from the
intended client network, using its configured DNS name and port. Load the
selected provider reference for the private endpoint and network path.

Use HC `status.kubeconfig` and HCP `status.kubeConfig` as publication signals,
not proof of guest API reachability. Their availability can unlock worker
bootstrap before all control-plane components finish rolling out.

## P5: Availability versus rollout completion

Read HCP `Available` type/status and require its condition's
`observedGeneration` to equal HCP `metadata.generation`. For
`ControlPlaneComponentsNotAvailable`, inspect the named CPCs; for an
endpoint/foundation condition, return to that gate. The HC target is
`spec.controlPlaneRelease.image` when set, otherwise `spec.release.image`.
Compare that target with HCP control-plane desired/history image and semantic
version; the data-plane release alone is not the control-plane target. If
pullspecs differ by tag/digest, resolve their equivalence before accepting the
gate; otherwise leave target matching unverified.

```bash
set -euo pipefail
mgmt_oc -n "$HCP_NAMESPACE" get controlplanecomponents -o json \
  > "$RESTRICTED_DIR/controlplanecomponents.raw.json" \
  2> "$RESTRICTED_DIR/controlplanecomponents.raw.stderr"
jq -r '.items[] | .metadata.name as $name | .status.conditions[]? |
  select(.type == "Available" or .type == "RolloutComplete") |
  [$name,.type,.status,.lastTransitionTime,.observedGeneration] | @tsv' \
  "$RESTRICTED_DIR/controlplanecomponents.raw.json"
mgmt_oc -n "$HC_NAMESPACE" get hostedcluster "$HC_NAME" -o json \
  2> "$RESTRICTED_DIR/hc-control-plane-target-query.raw.stderr" |
  jq '{requestedImage: (.spec.controlPlaneRelease.image // .spec.release.image)}' \
  > "$RESTRICTED_DIR/hc-control-plane-target.raw.json" \
  2> "$RESTRICTED_DIR/hc-control-plane-target-format.raw.stderr"
mgmt_oc -n "$HCP_NAMESPACE" get hostedcontrolplane "$HC_NAME" -o json \
  > "$RESTRICTED_DIR/hcp-control-plane-version.raw.json" \
  2> "$RESTRICTED_DIR/hcp-control-plane-version.raw.stderr"
jq '[.status.conditions[]? |
  {type, status, reason, message, lastTransitionTime, observedGeneration}]' \
  "$RESTRICTED_DIR/hcp-control-plane-version.raw.json" \
  > "$RESTRICTED_DIR/hcp-condition-details.raw.json"
jq -n --slurpfile hc "$RESTRICTED_DIR/hc-control-plane-target.raw.json" \
  --slurpfile hcp "$RESTRICTED_DIR/hcp-control-plane-version.raw.json" '
  ($hc[0].requestedImage) as $requestedImage |
  ($hcp[0].status.controlPlaneVersion // {}) as $versionStatus |
  ($versionStatus.history[0] // {}) as $history |
  ($hcp[0].status.conditions // [] |
    map(select(.type == "Available")) | first) as $available |
  {
    availableStatus: ($available.status // "Missing"),
    availableFresh: ($available != null and
      $available.observedGeneration == $hcp[0].metadata.generation),
    controlPlaneVersionFresh: ($versionStatus.observedGeneration ==
      $hcp[0].metadata.generation),
    desiredVersion: $versionStatus.desired.version,
    historyVersion: ($history.version // null),
    historyState: ($history.state // "Missing"),
    hcpSpecImageMatchesRequested: (($hcp[0].spec.controlPlaneReleaseImage // $hcp[0].spec.releaseImage) == $requestedImage),
    requestedImageMatchesDesired: ($requestedImage != null and
      $requestedImage == $versionStatus.desired.image),
    historyImageMatchesDesired: ($history.image != null and
      $history.image == $versionStatus.desired.image),
    historyVersionMatchesDesired: ($history.version != null and
      $history.version == $versionStatus.desired.version)
  }'
```

The HCP reason/message projection is restricted; use it privately to inspect a
specific failed condition, and redact before sharing. `WaitingForDependencies`
directs inspection upstream; `WaitingForOperands` can mean a primary workload
is ready while a child operand is not. Component registration order is not
boot order. Follow dependency status/version rather than restarting every
component downstream of an unready dependency.

HC `Available` mirrors HCP availability; neither checks workers. CPC
`Available=True` alone is weaker than `RolloutComplete=True` at the desired
component version. Accept HCP rollout completion only when the latest history
entry is `Completed`, its image and semantic version match the desired target,
and control-plane version status is current for the HCP generation. Use
version-matched source/CRDs if this component API differs or is absent in the
deployed release; API discovery failure is not a failed component condition.
