# Capture evidence without changing the experiment

For a quick diagnosis, save a small timestamped set of the relevant objects,
conditions, events and logs. A continuous collector is useful when the task
asks to watch startup, retain durations, or reproduce a transition. Do not
start a new cluster merely to acquire a timeline for an existing failure.

Treat command output, logs, provider responses, console output and collector
observations as potentially sensitive. Use a new local run directory created
by intake with mode `0700` under `umask 077`. Do not reuse an existing path:
`umask` and `mkdir -p` do not change its owner or permissions. If a workflow
must reuse a directory, verify that it is owned by the current user and has
mode `0700` before writing. Redirect raw stdout and stderr there instead of
streaming them to a terminal transcript or chat. Redact tokens, passwords, API
keys, session data, internal hostnames, PII
and customer data before printing or retaining output in ordinary evidence,
bundles or shared reports. Keep unredacted captures only in approved
access-controlled storage, separate from redacted evidence, and remove
temporary captures when no longer needed.

Keep each lifecycle attempt in a separate directory with HC UID, namespace/
name, infrastructure ID, provider, region/location, release tag and resolved
digest, HO image/source revision, release CPO image/source revision, NodePool
topology, endpoint access, management-cluster context, and collection times.
Keep HC UIDs, infrastructure IDs, endpoint-access details and context names in
access-controlled run metadata; record interventions explicitly. Never merge
clusters with reused names or mix management and guest observations under an
unlabeled kubeconfig.

If the available HyperShift checkout contains the timeline collector, use it
only when its `--help` supports the selected management context as well as the
kubeconfig. The command below assumes the collector spells that option
`--context`; substitute its documented spelling. If the collector cannot
target `MGMT_CONTEXT` explicitly, use the `mgmt_oc` snapshots instead of
relying on a different current context. The collector needs Python 3.11+ and
`oc`.
`HYPERSHIFT_REPO` is the resolved existing checkout, not an assumed path:

```bash
umask 077
if [[ -e "$RESTRICTED_DIR/startup" || -L "$RESTRICTED_DIR/startup" ]]; then
  echo "Use a fresh startup output path inside RESTRICTED_DIR." >&2
  exit 1
fi
if [[ -e "$RESTRICTED_DIR/startup-command.raw.log" ||
      -L "$RESTRICTED_DIR/startup-command.raw.log" ]]; then
  echo "Use a fresh command-log path inside RESTRICTED_DIR." >&2
  exit 1
fi
if [[ -n "$MGMT_CONTEXT" ]]; then
  python3 "$HYPERSHIFT_REPO/hack/cluster-startup-timeline.py" \
    --kubeconfig "$MGMT_KUBECONFIG" --context "$MGMT_CONTEXT" \
    --namespace "$HC_NAMESPACE" --name "$HC_NAME" \
    --interval 10 --timeout 45m --output-dir "$RESTRICTED_DIR/startup" \
    > "$RESTRICTED_DIR/startup-command.raw.log" 2>&1
else
  python3 "$HYPERSHIFT_REPO/hack/cluster-startup-timeline.py" \
    --kubeconfig "$MGMT_KUBECONFIG" \
    --namespace "$HC_NAMESPACE" --name "$HC_NAME" \
    --interval 10 --timeout 45m --output-dir "$RESTRICTED_DIR/startup" \
    > "$RESTRICTED_DIR/startup-command.raw.log" 2>&1
fi
```

Add `--guest-kubeconfig "$GUEST_KUBECONFIG"` when the matching config is
available. For capture begun before creation, use the collector's supported
late-loading workflow if available; do not create a placeholder that can
accidentally fall back to another cluster. With no collector checkout, retain
scoped timestamped snapshots instead; core diagnosis does not depend on it.

### Resume after gaps

When resuming an existing capture, allow only one writer per capture directory,
verify that its recorded HC UID is still current, and record a failed query as
an unknown observation rather than as absence or a negative condition:

```bash
set -euo pipefail
umask 077
capture_dir="$RESTRICTED_DIR/startup"
owner="$RESTRICTED_DIR/startup-owner.json"
lock="$RESTRICTED_DIR/startup.lock"
if [[ ! -d "$capture_dir" ]]; then
  echo "The capture directory does not exist; do not create a second one." >&2
  exit 1
fi
if ! mkdir "$lock" 2>/dev/null; then
  echo "Could not acquire $lock; check for another writer or permissions." >&2
  exit 1
fi
trap 'rmdir "$lock" 2>/dev/null || true' EXIT
if ! hc_uid=$(mgmt_oc --request-timeout=20s -n "$HC_NAMESPACE" \
  get hostedcluster "$HC_NAME" -o jsonpath='{.metadata.uid}' \
  2> "$RESTRICTED_DIR/startup-uid-query.raw.stderr"); then
  jq -nc '{observedAt: (now | todate), outcome: "unknown",
    reason: "hc_uid_query_failed_or_timed_out"}' \
    >> "$capture_dir/observation-gaps.raw.jsonl"
  echo "Could not read the current HC UID; recorded an observation gap." >&2
  exit 1
fi
if [[ -f "$owner" ]]; then
  recorded_uid=$(jq -r '.hcUID // empty' "$owner")
  if [[ "$recorded_uid" != "$hc_uid" ]]; then
    echo "Capture belongs to a different HC UID; start a separate run." >&2
    exit 1
  fi
else
  jq -n --arg hcUID "$hc_uid" '{hcUID: $hcUID}' > "$owner"
fi
if ! mgmt_oc --request-timeout=20s -n "$HC_NAMESPACE" \
  get hostedcluster "$HC_NAME" -o json \
  > "$capture_dir/hc-resume-query.raw.json" \
  2> "$capture_dir/hc-resume-query.raw.stderr"; then
  jq -nc --arg hcUID "$hc_uid" '{hcUID: $hcUID, observedAt: (now | todate),
    outcome: "unknown", reason: "query_failed_or_timed_out"}' \
    >> "$capture_dir/observation-gaps.raw.jsonl"
fi
```

The lock and owner file make a resumed capture attributable to one HC and one
writer. Hold the lock for the lifetime of the resumed collector, not only for
the preflight query; the `EXIT` trap releases it when that writer exits. If the
owner file is absent, adopt the capture only after confirming that its existing
observations or metadata record the same HC UID; otherwise start a new capture
directory. A timeout is retained as an observation gap; it does not prove that
a condition is false, a resource is absent, or the cluster is stuck. A lock
left behind by a crashed collector must be checked against running collector
processes before removal; do not remove a lock held by a live writer.

Before retaining collector output such as `observations.jsonl`, snapshots,
reports or command logs in the run bundle, redact sensitive values while
preserving the fields needed for timing and gate analysis. Keep only redacted
copies in ordinary evidence; keep unredacted working captures only in approved
restricted storage. Record API creation/transition/completion timestamps where
available; otherwise label first polling observation.
For creation, keep command start → HC creation separate from HC creation →
readiness. For deletion, keep command start, API deletion timestamp, command
return and independently verified absence separate. Exclude review/idle time
from both clocks. Retain failed pre-HC attempts separately from startup timing.

Polling includes query duration and is not an atomic snapshot. Conditions may
lag workloads. Preserve first successful observations and later regressions;
the latest `lastTransitionTime` is not necessarily the first success. Current
readiness must be checked against the latest intended generation/version.
Insufficient or failed observations remain unknown, not ready or absent.

Use restrictive directory/file permissions for captures. Keep raw credentials,
guest kubeconfigs and Secret data outside ordinary evidence bundles and git.
Keep HC UIDs and infrastructure IDs in access-controlled run metadata when
needed to distinguish recreated clusters; omit endpoint hosts, URLs and secret
references from shared evidence unless explicitly required and approved.
Redact logs and provider console output before printing, retaining in ordinary
evidence, or sharing; do not emit raw output and rely on a later review.
Save the dataset paths in the result and say whether they are local, committed,
or uploaded; a pushed documentation change is not a backup of ignored raw data.

When comparing runs, preserve configuration, version and intervention
differences and the number of observations. Two samples on a warm management
cluster are observations, not a production timing SLO or timeout rule.

Optional deeper public reference:
[Hosted cluster startup roadmap](https://hypershift.pages.dev/reference/cluster-startup-roadmap/)
(dependency graph, stage map, "Where am I?", measuring startup, code map),
[AWS startup debugging examples](https://hypershift.pages.dev/reference/cluster-startup-aws-observation/),
[AWS creation and deletion observations](https://hypershift.pages.dev/reference/cluster-startup-aws-repeat/),
and [AWS teardown roadmap](https://hypershift.pages.dev/reference/cluster-teardown-roadmap/).
The AWS startup and observation pages are 5.0.0-rc.7 development examples; the
teardown roadmap links measured teardown examples. None are generic timing
baselines. These links are optional; do not require network access for basic
use of this self-contained skill.
