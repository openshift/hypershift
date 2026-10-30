# Guide an authorized setup

Resolve the intended provider, management kubeconfig/context, namespace/name,
release and HO image, region/location, endpoint access, worker count/size,
availability policy, pull-secret location, and provider infrastructure/identity
inputs. Reuse information already supplied; ask only for inputs that affect
the concrete setup. Do not install HO or create resources for a planning-only
request.

When the task includes live inspection or execution, inspect the selected
management cluster and existing target resources. For local planning without
cluster access, defer those checks. Confirm that an existing HC of the same
name is the intended target before continuing. Record its UID when adopting
an existing setup; keep separate run directories and identities if a new
cluster replaces one previously observed.
Before live commands that produce runtime output, create the restricted local
directory described in the evidence reference and set `RESTRICTED_DIR`.

Use the user's workflow and scripts. If script changes are needed, honor the
requested copy/location policy and inspect a concrete local copy before running
it. Preserve the selected images rather than accepting a script's old defaults.
Do not silently switch providers, management clusters, or source checkouts.

## Release preflight

Record the source branch's release line as `SOURCE_MINOR` (for a
`release-X.Y` branch, `X.Y`; otherwise use the supported minor documented for
that revision). Resolve the requested release for the worker architecture, not
the host architecture. `oc adm release info` can select the host platform from
a multi-architecture image; on an Apple Silicon host that may be
`linux/arm64` while an AWS NodePool defaults to `linux/amd64`. Set
`WORKER_ARCH` from the requested NodePool and keep the architecture-specific
release digest separate from the multi-architecture index digest:

```bash
set -euo pipefail
umask 077
: "${SOURCE_MINOR:?set from the source branch release line}"
: "${WORKER_ARCH:?set from NodePool.spec.arch}"
: "${RELEASE_IMAGE:?set to the selected release image}"
oc adm release info "$RELEASE_IMAGE" \
  --filter-by-os="linux/$WORKER_ARCH" -o json \
  > "$RESTRICTED_DIR/release-info.raw.json" \
  2> "$RESTRICTED_DIR/release-info.raw.stderr"
jq --arg sourceMinor "$SOURCE_MINOR" --arg workerArch "$WORKER_ARCH" '{
  sourceMinor: $sourceMinor,
  workerArch: $workerArch,
  releaseVersion: .metadata.version,
  selectedPayloadDigest: .digest,
  multiArchIndexDigest: .listDigest
}' "$RESTRICTED_DIR/release-info.raw.json"
```

Retain both digests in run metadata. The selected payload digest identifies the
release manifest used for the worker architecture; the index digest identifies
the multi-architecture image that contains it. Check the resolved release
against the source branch's supported minor before creating the cluster.

Read the selected provider reference to check the relevant prerequisites.
Verify CLI flags against the actual local CLI's `--help` and version. Follow
available provider setup documentation/skills for that version; do not invent
a universal `create cluster` command or require another skill to exist.
When the workflow requires it, use supported render/dry-run output to review
the selected configuration locally. Keep credential-bearing output private.

If HO is already healthy, establish whether its image/configuration satisfies
the task. Do not reinstall or restart it merely to gather another timing sample.
If installation/change is needed and authorized, use the selected workflow and
verify its rollout before attributing later startup symptoms to the new HC.

For monitoring or timing requests, start capture before creation and retain
the create command's start/return times separately from HC creation. Use the
evidence reference for the collector or scoped timestamped snapshots. Start
guest observations when a matching new guest kubeconfig is available; do not
reuse a predecessor cluster's Secret or restart capture unnecessarily.

Run the authorized create workflow once. Capture stdout and stderr in
restricted local files under the evidence guidance, and record the return
status separately; do not stream raw output into a shared transcript. Redact
sensitive values before printing or retaining output in ordinary evidence.
If it fails, identify the failed step, allocated infrastructure ID, cloud
resources and any HC UID in access-controlled run metadata before choosing a
retry. A missing HC is not evidence that nothing was allocated. Resolve or
clean up the exact partial attempt within the existing authorization before
retrying; do not launch a loop that allocates another VPC/resource group on
every failure.

Once HC exists, use intake and the routing table to track control-plane,
worker, and guest gates. Continue to the requested target. A control plane
that is available is an intermediate result for a full cluster request.
If the work is blocked, retain evidence and give the precise next action.
Do not repeatedly create replacement clusters to avoid diagnosing the blocker.

At the handoff, state whether the cluster is retained or removed and identify
the saved evidence location. Tear down only when the task includes it; use
the normal supported path and the teardown reference if cleanup is requested.
