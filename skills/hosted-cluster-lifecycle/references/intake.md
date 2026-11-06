# Identify the target and the current gates

Resolve these from the request and existing session before asking for missing
information: plan/create/monitor/debug, management kubeconfig and optional
context, HC namespace/name, provider, release/HO images, intended NodePools,
and readiness target. Do not use remembered personal kubeconfig paths or old
experiment images as defaults. For an offline request, use supplied snapshots
and identify their collection times and HC UID; do not contact a cluster.

For live work, define these from the resolved target:

```bash
MGMT_KUBECONFIG=/path/from-the-current-task
MGMT_CONTEXT=${MGMT_CONTEXT:-}
HC_NAMESPACE=selected-namespace
HC_NAME=selected-cluster
RESTRICTED_DIR=/path/to/new-private-local-directory
```

The helper below applies the explicit kubeconfig and optional context to every
management-cluster command. When `MGMT_CONTEXT` is empty, it uses the selected
kubeconfig's current context. Keep guest commands separate; do not change the
user's global current context to make these examples work.

Take a minimal snapshot with `oc` and `jq`. Run this in Bash:

```bash
set -euo pipefail
umask 077
if [[ -e "$RESTRICTED_DIR" || -L "$RESTRICTED_DIR" ]]; then
  echo "Choose a new RESTRICTED_DIR; existing directories are not assumed private." >&2
  exit 1
fi
mkdir -m 700 "$RESTRICTED_DIR"

mgmt_oc() {
  if [[ -n "$MGMT_CONTEXT" ]]; then
    oc --kubeconfig "$MGMT_KUBECONFIG" --context "$MGMT_CONTEXT" "$@"
  else
    oc --kubeconfig "$MGMT_KUBECONFIG" "$@"
  fi
}

mgmt_oc config view --minify -o jsonpath='{.contexts[0].name}' \
  > "$RESTRICTED_DIR/current-context.raw" \
  2> "$RESTRICTED_DIR/current-context.raw.stderr"
printf '\n' >> "$RESTRICTED_DIR/current-context.raw"

if ! hc_json=$(mgmt_oc -n "$HC_NAMESPACE" \
  get hostedcluster "$HC_NAME" --ignore-not-found -o json \
  2> "$RESTRICTED_DIR/hc-query.raw.stderr"); then
  echo "HostedCluster query failed; inspect its restricted stderr file for access, discovery, or connectivity errors." >&2
  exit 1
fi
if [[ -z "$hc_json" ]]; then
  printf 'HostedCluster %s/%s: NotFound\n' "$HC_NAMESPACE" "$HC_NAME"
else
  if ! hc_snapshot=$(printf '%s\n' "$hc_json" | jq '
    .metadata.generation as $generation | {
    metadata: (.metadata | {name, namespace, generation,
      creationTimestamp, deletionTimestamp}),
    platformType: .spec.platform.type,
    status: {
      releaseVersion: .status.version.desired.version,
      conditions: [.status.conditions[]? | {
        type, status, lastTransitionTime, observedGeneration,
        fresh: (.observedGeneration == $generation)
      }]
    }}' 2> "$RESTRICTED_DIR/hc-snapshot.raw.stderr"); then
    echo "Could not format the HostedCluster snapshot." >&2
    exit 1
  fi
  printf '%s\n' "$hc_snapshot"
fi

HCP_NAMESPACE="${HC_NAMESPACE}-${HC_NAME//./-}"
if ! hcp_json=$(mgmt_oc -n "$HCP_NAMESPACE" \
  get hostedcontrolplane "$HC_NAME" --ignore-not-found -o json \
  2> "$RESTRICTED_DIR/hcp-query.raw.stderr"); then
  echo "HostedControlPlane query failed; inspect its restricted stderr file for access, discovery, or connectivity errors." >&2
  exit 1
fi
if [[ -z "$hcp_json" ]]; then
  printf 'HostedControlPlane %s/%s: NotFound\n' "$HCP_NAMESPACE" "$HC_NAME"
else
  if ! hcp_snapshot=$(printf '%s\n' "$hcp_json" | jq '
    .metadata.generation as $generation | {
    metadata: (.metadata | {name, namespace, generation}),
    status: {
      ready: .status.ready,
      initialized: .status.initialized,
      version: .status.version,
      conditions: [.status.conditions[]? | {
        type, status, lastTransitionTime, observedGeneration,
        fresh: (.observedGeneration == $generation)
      }]
    }}' 2> "$RESTRICTED_DIR/hcp-snapshot.raw.stderr"); then
    echo "Could not format the HostedControlPlane snapshot." >&2
    exit 1
  fi
  printf '%s\n' "$hcp_snapshot"
fi

if ! nodepool_json=$(mgmt_oc -n "$HC_NAMESPACE" \
  get nodepools -o json \
  2> "$RESTRICTED_DIR/nodepools-query.raw.stderr"); then
  echo "NodePool query failed; inspect its restricted stderr file for access, discovery, or connectivity errors." >&2
  exit 1
fi
if ! nodepool_snapshot=$(printf '%s\n' "$nodepool_json" | jq --arg hc "$HC_NAME" \
  '[.items[] | select(.spec.clusterName == $hc) |
    .metadata.generation as $generation | {
      name: .metadata.name, generation: $generation,
      desiredReplicas: .spec.replicas,
      autoScaling: (if .spec.autoScaling == null then null else {
        min: .spec.autoScaling.min, max: .spec.autoScaling.max
      } end),
      status: {
        replicas: .status.replicas,
        version: .status.version,
        conditions: [.status.conditions[]? | {
          type, status, lastTransitionTime, observedGeneration,
          fresh: (.observedGeneration == $generation)
        }]
      }}]' 2> "$RESTRICTED_DIR/nodepools-snapshot.raw.stderr"); then
  echo "NodePool snapshot formatting failed; inspect its restricted stderr file." >&2
  exit 1
fi
printf '%s\n' "$nodepool_snapshot"
```

These snapshots use allowlisted fields: target names, generation and
timestamps, platform type, semantic versions, readiness flags, replica counts,
autoscaling bounds, and condition type/status/transition metadata with a
freshness comparison. They omit full status,
condition reasons/messages, endpoints, secret references, image locations,
UIDs and infrastructure IDs. Collect additional identifiers only when
required, in access-controlled run metadata; review sensitive fields before
saving or sharing. Capture raw pod, provider, console, create-command and query
output only in the restricted local directory; print only allowlisted
snapshots. Do not stream raw output into shared output.
Redact tokens, passwords, API keys, session data, internal hostnames, PII and
customer data before printing or retaining them in ordinary evidence, bundles
or shared reports. Keep unredacted captures only in approved access-controlled
storage, and remove temporary captures when no longer needed.

With `--ignore-not-found`, empty output is reported explicitly as `NotFound`; a
nonzero `oc` status remains a query error, with stderr saved in the restricted
directory for private inspection.
The NodePool query can succeed with an empty filtered list, which is
different from a failed query. Do not suppress command errors or convert
them to empty snapshots.

If HC is absent, check the exact create command and its logs before P0.
Cloud resources may already have been allocated. A permission, transport, or
discovery failure is different from a successful NotFound response.

Derive HCP namespace using the naming rule above and verify against discovered
objects if a version or deployment differs. Discover the HO deployment and
namespace rather than assuming `hypershift/operator` always exists. The
common HCP deployments are `control-plane-operator`, `cluster-api`, and
`capi-provider`. Use fully qualified CAPI resource names to avoid confusing
them with OpenShift `Cluster` or Machine API resources.

Read `status.conditions` as a map keyed by `type`. The example captures
condition type/status, transition time and observed generation; it excludes
reason/message fields. If deeper diagnosis needs those fields or related pod,
provider, console or command logs, collect only the necessary details using
approved access controls. Review and redact endpoint URLs, identifiers,
credentials and secret references before printing or retaining them in
ordinary evidence or sharing.
For a positive readiness gate, require the relevant condition to be `True` and
fresh for the current generation; a missing `observedGeneration` is unverified.
Missing optional conditions are not failures, and some healthy conditions are
intentionally absent. Interpret polarity: `Progressing=False` and
`Degraded=False` are not failed gates.

Choose the relevant workstreams rather than inferring everything from HC
`Available`: HCP/HC availability concerns control-plane usability; component
rollout completion adds version convergence; NodePool readiness concerns
workers; guest CVO/operators determine the remaining full-cluster target.
If worker intent is unknown, establish it before diagnosing absent NodePools
as a fault. Control-plane-only and deliberately zero-replica configurations
must not cause the agent to create workers without instruction.
