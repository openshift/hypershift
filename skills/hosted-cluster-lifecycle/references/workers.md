# P6–P8: Bootstrap, cloud machines and usable workers

Select only NodePools with `spec.clusterName == HC_NAME`, then inspect the
pool named by the failing aggregate message. Run intake first in the same Bash
session; management commands below use its `mgmt_oc` helper. The derived HCP
namespace scopes CAPI objects to this HC, but can contain multiple pools.
Guest Nodes are scoped by `hypershift.openshift.io/nodePool`; Machines are
scoped through the selected pool's MachineDeployment/MachineSet ownership.

## P6: No machine workload yet

```bash
set -euo pipefail
umask 077
mgmt_oc -n "$HC_NAMESPACE" get nodepool "$NODEPOOL_NAME" -o json > "$RESTRICTED_DIR/nodepool.raw.json" 2> "$RESTRICTED_DIR/nodepool.raw.stderr"
jq '[.status.conditions[]? | {type, status, reason, message, lastTransitionTime, observedGeneration}]' "$RESTRICTED_DIR/nodepool.raw.json" > "$RESTRICTED_DIR/nodepool-condition-details.raw.json"
jq '
  .metadata.generation as $generation | {
    name: .metadata.name, generation: $generation,
    releaseImagePresent: (.spec.release.image != null),
    configuredReplicas: .spec.replicas,
    autoScaling: (if .spec.autoScaling == null then null else
      {min: .spec.autoScaling.min, max: .spec.autoScaling.max}
    end),
    status: {availableReplicas: .status.replicas, version: .status.version,
      conditions: [.status.conditions[]? | {
        type, status, lastTransitionTime, observedGeneration,
        fresh: (.observedGeneration == $generation)
      }]}
  }' "$RESTRICTED_DIR/nodepool.raw.json"

mgmt_oc -n "$HC_NAMESPACE" get hostedcluster "$HC_NAME" -o json > "$RESTRICTED_DIR/hostedcluster-worker-gate.raw.json" 2> "$RESTRICTED_DIR/hostedcluster-worker-gate.raw.stderr"
jq '{kubeconfigPublished: (.status.kubeconfig.name != null), platformType: .spec.platform.type}' "$RESTRICTED_DIR/hostedcluster-worker-gate.raw.json"
```

The NodePool condition reason/message projection stays in the restricted
directory; use it privately when the safe status view identifies a failing
gate. Do not print condition messages or raw objects into ordinary evidence.
An empty initial `status.version` is expected until rollout completes.
Resolve `.spec.release.image` to its semantic version from release metadata
before comparing it with `status.version`.

Once HC kubeconfig is published, the NodePool controller generates token and
user-data Secrets and a platform template. Replace upgrades normally use a
MachineDeployment; InPlace uses a MachineSet. Inspect Secret names, creation
times and expected annotations only, never their data or bootstrap tokens.

On AWS, a generated payload can coexist with `AWSSecurityGroupAvailable=False`
and `DefaultSGNotReady`. Load the AWS reference before investigating a worker
that has not yet been created. A ready `AWSCluster` does not prove the default
worker SG has been published.

If bootstrap/workload objects are still missing after their prerequisites,
inspect HO's pool-specific reconciliation errors rather than guest kubelet logs.

## Select the pool's CAPI machines

Run this helper block after intake and before P7 or P8, in the same Bash
session. It selects the
MachineDeployment named for the NodePool, the NodePool-owned MachineSet, and
MachineSets labeled for that MachineDeployment. It then includes only Machines
whose owner reference points to one of those selected MachineSets. Condition
reasons/messages and machine names remain in restricted files; the printed
summary contains only counts and readiness fields.

```bash
capture_nodepool_capi() {
  local pool_ref="$HC_NAMESPACE/$NODEPOOL_NAME"
  local machine_set_names

  mgmt_oc -n "$HCP_NAMESPACE" get machinedeployments.cluster.x-k8s.io,machinesets.cluster.x-k8s.io -o json > "$RESTRICTED_DIR/nodepool-workloads.raw.json" 2> "$RESTRICTED_DIR/nodepool-workloads.raw.stderr"
  jq --arg pool "$NODEPOOL_NAME" --arg pool_ref "$pool_ref" '
    [.items[] | select(
      (.kind == "MachineDeployment" and .metadata.name == $pool and
        .metadata.annotations["hypershift.openshift.io/nodePool"] == $pool_ref) or
      (.kind == "MachineSet" and
        (.metadata.annotations["hypershift.openshift.io/nodePool"] == $pool_ref or
         .metadata.labels["cluster.x-k8s.io/deployment-name"] == $pool))
    ) | {
      kind, name: .metadata.name,
      specReplicas: .spec.replicas,
      replicas: .status.replicas,
      readyReplicas: .status.readyReplicas,
      availableReplicas: .status.availableReplicas,
      conditions: [.status.conditions[]? |
        {type, status, reason, message, lastTransitionTime, observedGeneration}]
    }]' "$RESTRICTED_DIR/nodepool-workloads.raw.json" > "$RESTRICTED_DIR/nodepool-workload-details.raw.json"
  jq '[.[] | {kind, specReplicas, replicas, readyReplicas, availableReplicas}]' "$RESTRICTED_DIR/nodepool-workload-details.raw.json"
  machine_set_names=$(jq -c '[.[] | select(.kind == "MachineSet") | .name]' "$RESTRICTED_DIR/nodepool-workload-details.raw.json")

  mgmt_oc -n "$HCP_NAMESPACE" get machines.cluster.x-k8s.io -o json > "$RESTRICTED_DIR/capi-machines.raw.json" 2> "$RESTRICTED_DIR/capi-machines.raw.stderr"
  jq --argjson machine_set_names "$machine_set_names" '
    [.items[] | select(any(.metadata.ownerReferences[]?;
      .kind == "MachineSet" and
      (.name as $owner_name |
        ($machine_set_names | index($owner_name)) != null))) | {
      name: .metadata.name,
      phase: (.status.phase // "Unknown"),
      deletionTimestampPresent: (.metadata.deletionTimestamp != null),
      providerIDPresent: (.status.providerID != null),
      nodeRefPresent: (.status.nodeRef != null),
      conditions: [.status.conditions[]? |
        {type, status, reason, message, lastTransitionTime, observedGeneration}]
    }]' "$RESTRICTED_DIR/capi-machines.raw.json" > "$RESTRICTED_DIR/nodepool-machine-details.raw.json"
  jq '{total: length,
    phases: (group_by(.phase) | map({phase: .[0].phase, count: length})),
    withProviderID: ([.[] | select(.providerIDPresent)] | length),
    withNodeRef: ([.[] | select(.nodeRefPresent)] | length),
    conditions: ([.[] | .conditions[]? | select(.status != "True") | {type, status}] | group_by(.type) | map({type: .[0].type, count: length}))}' "$RESTRICTED_DIR/nodepool-machine-details.raw.json"
}
```

The workload's `.spec.replicas` is the current effective target, including for
an autoscaled pool; the autoscaler can change it. NodePool `.status.replicas`
and workload `availableReplicas` describe current availability, not a fixed
autoscaler target. Keep the NodePool min/max bounds with the observation.

## P7: Cloud provisioning and ignition

```bash
set -euo pipefail
umask 077
capture_nodepool_capi
mgmt_oc -n "$HCP_NAMESPACE" logs deployment/capi-provider -c manager --since=10m --tail=200 > "$RESTRICTED_DIR/capi-provider.raw.log" 2>&1
mgmt_oc -n "$HCP_NAMESPACE" get events --sort-by=.lastTimestamp -o json > "$RESTRICTED_DIR/worker-events.raw.json" 2> "$RESTRICTED_DIR/worker-events.raw.stderr"
jq '[.items[] | {type, reason, firstTimestamp, lastTimestamp, count, objectKind: .involvedObject.kind, objectNamePresent: (.involvedObject.name != null)}]' "$RESTRICTED_DIR/worker-events.raw.json"
```

Inspect AWSMachine or AzureMachine status and provider ID in restricted local
context to correlate the selected Machine with its instance/VM; omit exact IDs
from shared evidence unless explicitly required and approved. Capture any
provider error under the evidence log-handling guidance. If no instance exists,
distinguish IAM/quota/image/network provisioning errors before investigating
an ignition download.

`ReachedIgnitionEndpoint=True` means at least one instance fetched a payload.
It does not prove all machines fetched it, a Node registered, or kubelet is
healthy. Match each selected Machine/provider ID to its cloud instance and
eventual Node. Boot-time MachineConfig application and a reboot can occur
after the ignition fetch but before a CSR/Node appears. Inspect console/boot
progress before concluding that the missing Node proves ignition failure.

## P8: Machine and Node readiness

```bash
set -euo pipefail
umask 077
capture_nodepool_capi
mgmt_oc -n "$HC_NAMESPACE" get nodepool "$NODEPOOL_NAME" -o json > "$RESTRICTED_DIR/nodepool.raw.json" 2> "$RESTRICTED_DIR/nodepool.raw.stderr"
jq '[.status.conditions[]? | {type, status, reason, message, lastTransitionTime, observedGeneration}]' "$RESTRICTED_DIR/nodepool.raw.json" > "$RESTRICTED_DIR/nodepool-condition-details.raw.json"
jq '.metadata.generation as $generation | {version: .status.version, availableReplicas: .status.replicas, configuredReplicas: .spec.replicas, autoScaling: (if .spec.autoScaling == null then null else {min: .spec.autoScaling.min, max: .spec.autoScaling.max} end), conditions: [.status.conditions[]? | {type, status, lastTransitionTime, observedGeneration, fresh: (.observedGeneration == $generation)}]}' "$RESTRICTED_DIR/nodepool.raw.json"
oc --kubeconfig "$GUEST_KUBECONFIG" get nodes -l "hypershift.openshift.io/nodePool=$NODEPOOL_NAME" -o json 2> "$RESTRICTED_DIR/nodepool-nodes-query.raw.stderr" | jq --arg pool "$NODEPOOL_NAME" '[.items[] | select(.metadata.labels["hypershift.openshift.io/nodePool"] == $pool) | {name: .metadata.name, deletionTimestampPresent: (.metadata.deletionTimestamp != null), conditions: [.status.conditions[]? | {type, status, reason, message, lastTransitionTime, observedGeneration}]}]' > "$RESTRICTED_DIR/nodepool-nodes.raw.json" 2> "$RESTRICTED_DIR/nodepool-nodes-format.raw.stderr"
jq '{total: length,
  ready: ([.[] | select(.deletionTimestampPresent == false and any(.conditions[]?; .type == "Ready" and .status == "True"))] | length),
  terminatingReady: ([.[] | select(.deletionTimestampPresent == true and any(.conditions[]?; .type == "Ready" and .status == "True"))] | length),
  unresolved: ([.[] as $node | ($node.conditions | map(select(.type == "Ready")) | first) as $ready | select($ready == null or $ready.status != "True")] | length)}' "$RESTRICTED_DIR/nodepool-nodes.raw.json"

oc --kubeconfig "$GUEST_KUBECONFIG" get csr -o json > "$RESTRICTED_DIR/guest-csrs.raw.json" 2> "$RESTRICTED_DIR/guest-csrs.raw.stderr"
: > "$RESTRICTED_DIR/worker-kubelet-csr-details.raw.jsonl"
unparsed_worker_csrs=0
while IFS= read -r csr; do
  request=$(jq -r '.spec.request' <<< "$csr")
  if ! subject=$(printf '%s' "$request" | python3 -c 'import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))' | openssl req -noout -subject -nameopt RFC2253 2>> "$RESTRICTED_DIR/csr-subject.raw.stderr"); then
    unparsed_worker_csrs=$((unparsed_worker_csrs + 1))
    continue
  fi
  if [[ "$subject" == *"CN=system:node:"* && "$subject" == *"O=system:nodes"* ]]; then
    jq -c --arg subject "$subject" '{
      requester: .spec.username,
      subject: $subject,
      approved: any(.status.conditions[]?; .type == "Approved" and .status == "True"),
      denied: any(.status.conditions[]?; .type == "Denied" and .status == "True"),
      pending: ([.status.conditions[]? | select((.type == "Approved" or .type == "Denied") and .status == "True")] | length == 0)
    }' <<< "$csr" >> "$RESTRICTED_DIR/worker-kubelet-csr-details.raw.jsonl"
  fi
done < <(jq -c '.items[] | select(
  .spec.signerName == "kubernetes.io/kube-apiserver-client-kubelet" and
  (.spec.username // "" | startswith("system:bootstrap:")) and
  any(.spec.groups[]?; . == "system:bootstrappers"))' "$RESTRICTED_DIR/guest-csrs.raw.json")
jq -s --argjson unparsed "$unparsed_worker_csrs" '{scope: "guest-cluster", total: (length + $unparsed),
  unparsed: $unparsed,
  approved: ([.[] | select(.approved)] | length),
  denied: ([.[] | select(.denied)] | length),
  pending: ([.[] | select(.pending)] | length)}' "$RESTRICTED_DIR/worker-kubelet-csr-details.raw.jsonl"
```

P8 repeats the selected Machine/NodePool capture so its counts are from the
same observation. Only non-terminating Ready Nodes count toward the pool's
usable replica target; `terminatingReady` reports Ready Nodes being deleted
separately. Machine and Node names plus condition reasons/messages stay
in restricted files; redact them before printing or retaining them in ordinary
evidence. The CSR filter keeps only the kubelet client signer, bootstrap-token
requesters, and requests whose subject identifies `system:node:<name>` in
`system:nodes`; unrelated serving, renewal, or other CSR types do not affect
worker-bootstrap counts. If any candidate request cannot be parsed, the
summary reports it as `unparsed`; treat the CSR count as incomplete until
those requests can be inspected.

The CSR summary is cluster-wide across all NodePools. It does not establish
whether the selected pool has pending bootstrap requests. Correlate a request's
subject node name with the selected pool's Nodes or Machine identity before
using it as P8 evidence for that pool. If that association is unavailable,
keep its attribution unknown; another pool's pending CSR is not this pool's
blocker.

`AllMachinesReady` aggregates CAPI Machine readiness; `AllNodesHealthy`
aggregates NodeHealthy; NodePool `Ready` follows its CAPI workload. Require
fresh NodePool `Ready`, `AllMachinesReady` and `AllNodesHealthy` conditions,
then compare the resolved requested semantic version with `status.version` and
confirm `UpdatingVersion` and `UpdatingConfig` are not active. For fixed-size
pools compare NodePool readiness with `spec.replicas`; for autoscaled pools,
report the configured min/max and observed effective workload replicas rather
than treating omitted `spec.replicas` as zero. A running EC2 instance/VM is
weaker evidence than any of these gates.

For absent Nodes, follow boot progress, guest API reachability and correlated
bootstrap CSR details. For registered unhealthy Nodes, inspect
Ready/network/disk/memory conditions, CNI rollout and kubelet errors. Do not
blindly approve every CSR.

If guest access is unavailable, report what management-side status establishes
and what remains unverified. Do not substitute management-cluster Nodes for
guest Nodes. Once the requested workers are ready, use guest convergence for
the remaining full-cluster checks.
