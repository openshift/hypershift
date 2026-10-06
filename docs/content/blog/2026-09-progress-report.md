---
title: September 2026 Progress Report
description: Etcd snapshot restore split-brain fix, konnectivity tunnel reliability overhaul, operator startup deadlock, VPC endpoint state machine bug, Karpenter drift detection during upgrades, and a packed Beneath the Headlines — 276 PRs from 71 contributors.
---

# September 2026 Progress Report

<div class="grid cards" markdown>

-   :octicons-git-pull-request-24:{ .lg } **276** PRs merged
-   :octicons-people-24:{ .lg } **71** contributors
-   :octicons-alert-24:{ .lg } **0** breaking changes
-   :octicons-clock-24:{ .lg } **338.4h** avg merge time

</div>

HyperShift's August 23 to September 22 window was dominated by reliability fixes — the kind that don't show up in feature announcements but determine whether clusters actually work when things go wrong. An etcd snapshot restore bug that caused split-brain. A konnectivity tunnel loss that required restarting every agent pod. A circular dependency that could prevent the HyperShift operator from ever starting. A VPC endpoint deletion bug affecting 40% of ROSA HCP cluster teardowns. And a Karpenter drift detection race that prematurely replaced every worker node during control plane upgrades.

Seventy-one contributors participated across the hypershift, release, enhancements, and ai-helpers repositories. Fifty-three of the 276 merged PRs came from bots — cherry-picks, dependency bumps, CVE remediations, and automated fixes. Ten PRs addressed customer-reported bugs. The average time from PR open to merge was 338.4 hours, up from 240.6 last month, reflecting several long-lived PRs that carried over from previous cycles.

What follows are five stories chosen for their technical depth and the subtlety of the bugs they fixed.

---

## :material-database-alert: Etcd Snapshot Restore: Fixing Split-Brain and CrashLoops

Disaster recovery is one of those features you'd rather never test in production. But when you do, it needs to work on the first try — there's no "let me restore the restore." HyperShift's etcd snapshot restore path had a critical bug that could leave the cluster in a worse state than before the restore attempt.

The problem was deceptively simple. When `etcdctl snapshot restore` (or `etcdutl snapshot restore` on newer versions) restores a snapshot, it creates a fresh data directory with a new cluster identity. But the restore command needs to know *which member* it's restoring for — the member name, peer URL, and initial cluster membership. Without these flags, every restored pod thinks it's starting a brand-new single-member cluster. When three pods all do this simultaneously, you get split-brain: three independent single-member clusters, each with member ID `0`, each believing it's the sole member.

[@tony-schndr](https://github.com/tony-schndr)'s [PR #9048](https://github.com/openshift/hypershift/pull/9048) fixed this by passing `--name`, `--initial-advertise-peer-urls`, `--initial-cluster`, and `--initial-cluster-token` to the restore command. These flags are injected via environment variables derived from each pod's hostname and the hosted control plane's namespace, ensuring each restored member has a unique identity that matches the expected three-member cluster topology.

But the fix didn't stop at membership flags. The PR also reordered the init containers so that `etcd-init` runs *before* `reset-member`. The previous ordering caused a subtle member ID mismatch: `reset-member` would compute the member ID based on the old data directory, then `etcd-init` would restore a snapshot that created a *new* data directory with a different member ID. The restored etcd process would start with a member ID that didn't match what the cluster expected, causing it to be rejected and CrashLoop.

The third piece was the `--bump-revision 1000000000` and `--mark-compacted` flags. After a snapshot restore, the new cluster starts at the snapshot's revision number. But Kubernetes informer caches in the kube-apiserver and other controllers still hold watch bookmarks at *higher* revision numbers from before the restore. When these controllers reconnect to the restored etcd, they send a `resourceVersion` that's ahead of etcd's current revision. Etcd responds with a "compaction" error, and the controller falls back to a full re-list — which is correct behavior. But the revision bump ensures this happens cleanly by placing a compaction marker at a known-safe point, rather than relying on controllers to handle the edge case of a revision that went *backward*.

The companion [PR #9074](https://github.com/openshift/hypershift/pull/9074) by [@tony-schndr](https://github.com/tony-schndr) fixed a related but distinct bug in the backup path. The HCPEtcdBackup controller retrieves cloud credential Secrets from the informer cache to configure the backup destination. But informer caches have a startup window where newly created Secrets aren't yet visible. When the controller reconciled a backup during this window, it would get a NotFound error and permanently mark the backup as `BackupFailed` — a terminal condition that's never retried. The fix adds a fallback to the API reader (bypassing the cache) when the cached client returns NotFound, and changes the backup status to a retryable "waiting for credentials" state instead of a terminal failure.

[@jparrill](https://github.com/jparrill)'s [PR #9356](https://github.com/openshift/hypershift/pull/9356) closed the loop with a regression test. After each snapshot restore, the test now verifies that all three etcd members have the same cluster ID and unique member IDs — exactly the invariant that the split-brain bug violated. The test parses `etcdctl member list` output via `kubectl exec` and fails fast if any member reports a different cluster identity.

!!! danger "Production Impact"
    Before this fix, etcd snapshot restore in hosted control planes could leave the cluster in an unrecoverable split-brain state with three independent single-member clusters. The only recovery path was manual intervention by an SRE team to delete and recreate the etcd StatefulSet from scratch.

---

## :material-connection: Konnectivity Reliability: Tunnel Loss, DNS Fallback, and Certificate Splitting

Konnectivity is the tunnel that connects the hosted control plane to the guest cluster's worker nodes. When it breaks, `kubectl exec` stops working, webhooks fail, and VMs can't be restarted via the console. This month saw three coordinated improvements to konnectivity reliability, each addressing a different failure mode.

The most impactful was [@vsolanki12](https://github.com/vsolanki12)'s [PR #9260](https://github.com/openshift/hypershift/pull/9260), which added the `--sync-forever` flag to the konnectivity-agent in both the control-plane Deployment and the data-plane DaemonSet. The problem: when a konnectivity-agent loses its connection to the server (network blip, server pod restart, load balancer failover), it's supposed to re-establish the tunnel. But the upstream `apiserver-network-proxy` agent has a known issue where, under certain timing conditions, the reconnection logic gives up after exhausting its initial sync attempts. The tunnels are permanently lost until the pod is restarted.

The `--sync-forever` flag tells the agent to continuously attempt reconnection, indefinitely. The team considered and rejected two alternatives: a lease-based server counting mechanism (too complex for the OCP fork of the proxy) and a sidecar watcher process (additional resource overhead). The flag was extensively validated on a live HCP cluster against multiple known upstream issues before merging. The backport to release-5.0 ([PR #9430](https://github.com/openshift/hypershift/pull/9430)) had to be done manually because a syntax error in the `/jira backport` command prevented the cherry-pick robot from opening the PR — a reminder that CI automation has its own failure modes.

The second improvement was DNS-level resilience. [@vsolanki12](https://github.com/vsolanki12)'s [PR #9181](https://github.com/openshift/hypershift/pull/9181) replaced the single-IP DNS resolution in the konnectivity proxy with a multi-IP fallback mechanism. Previously, `resolvePreferIPv4()` returned a single IP address. If that IP became unreachable (stale DNS cache, failed node, load balancer health check lag), every connection through the proxy failed until DNS TTL expired and the resolver picked a different IP.

The new `resolveAllIPs()` returns all resolved IPs with IPv4 addresses first. But there's a constraint: the `socks5.NameResolver` interface only returns a single IP. The PR works around this by storing fallback IPs in the Go context via `contextWithFallbackIPs()`, then reading them back in `DialContext()` when the primary connection fails. Each fallback attempt goes through the full konnectivity TLS handshake, capped at 3 attempts to avoid excessive TLS overhead with large DNS round-robin responses. The `dialThroughKonnectivity()` helper was extracted as a pure refactor to make the retry logic readable.

The third piece was certificate architecture. [@vsolanki12](https://github.com/vsolanki12)'s [PR #9098](https://github.com/openshift/hypershift/pull/9098) replaced the single `konnectivity-signer` CA with four dedicated signers: `konnectivity-server-serving-signer`, `konnectivity-cluster-serving-signer`, `konnectivity-server-auth-signer`, and `konnectivity-client-auth-signer`. With a single CA, rotating or revoking any konnectivity certificate required rotating the entire CA — which invalidated *all* konnectivity certificates simultaneously. The four-signer model allows independent rotation of serving certificates (for TLS termination) without affecting auth certificates (for mutual TLS between agent and server), and vice versa.

The migration is backward-compatible: the legacy `konnectivity-signer` secret is preserved in the aggregate CA bundle ConfigMap alongside the four new signers. `ReconcileSignedCert` only re-signs certificates on expiry or invalidity, so the rollout doesn't trigger immediate cert churn. Existing deployments continue working with their current certificates until the next natural rotation cycle.

!!! tip "Key Takeaway"
    These three PRs address three layers of konnectivity failure: connection persistence (sync-forever), DNS-level redundancy (multi-IP fallback), and certificate lifecycle isolation (four dedicated signers). Together, they significantly reduce the blast radius of any single konnectivity component failure.

---

## :material-restart-alert: The HyperShift Operator Startup Deadlock

This bug is a textbook example of a circular dependency that only manifests under specific timing conditions — in this case, during CAPI v1beta1-to-v1beta2 conversion webhook initialization on Azure self-managed clusters.

The deadlock worked like this:

1. The HyperShift operator's **readiness probe** checked the `/metrics` endpoint.
2. The `/metrics` endpoint is served by controller-runtime's metrics server, which requires the **informer cache** to be synced.
3. The informer cache lists CAPI CRDs (MachineDeployment, Machine, etc.) that have both v1beta1 and v1beta2 versions. Listing these requires the API server to invoke a **conversion webhook**.
4. The conversion webhook is served by the HyperShift operator itself. But the webhook server only starts *after the pod is ready*.
5. The pod is only ready when the readiness probe passes, which requires `/metrics` to be up, which requires the cache to sync, which requires the webhook to be running, which requires the pod to be ready.

The result: the operator pod never becomes ready, Kubernetes restarts it after the liveness probe times out, and the cycle repeats. Management cluster creation is permanently blocked.

[@bryan-cox](https://github.com/bryan-cox)'s [PR #9387](https://github.com/openshift/hypershift/pull/9387) broke the cycle by replacing the `/metrics`-based probes with controller-runtime's dedicated health server. The liveness probe now uses `healthz.Ping` on `/healthz` — a simple "is the process alive" check that has no dependency on cache state. The readiness probe uses `/readyz` with `WebhookServer.StartedChecker()`, which verifies that the local TLS webhook listener is accepting connections. This means the pod becomes ready as soon as the webhook server starts, *before* the cache finishes syncing. The cache can then sync (invoking the now-running webhook), and `/metrics` becomes available for Prometheus scraping — but it's no longer on the critical path for pod readiness.

The companion [PR #9389](https://github.com/openshift/hypershift/pull/9389) added defense-in-depth for the metrics collector itself. The NodePool metrics collector calls `cache.List()` for HostedCluster, MachineSet, MachineDeployment, and NodePool resources during `/metrics` scrapes. If the cache hasn't synced (or is blocked on the same conversion webhook issue), these calls block indefinitely with `context.Background()`. The fix replaces the unbounded context with a 5-second timeout, so a blocked cache read returns an error instead of hanging the entire metrics scrape goroutine.

!!! info "Why This Only Surfaced Now"
    The deadlock requires CAPI CRDs with multiple served versions *and* a conversion webhook. This combination became common when CAPI v1beta2 landed alongside v1beta1, but only affects clusters where the HyperShift operator itself serves the conversion webhook — which is the Azure self-managed topology. AWS and other topologies use different webhook hosting arrangements that don't create this particular cycle.

---

## :material-delete-alert: VPC Endpoint State Machine: A 40% Deletion Failure Rate

Sometimes the most impactful bugs are the simplest. The VPC endpoint deletion logic in HyperShift's `awsprivatelink` package had been treating the *existence* of a VPC endpoint in the `DescribeVpcEndpoints` response as evidence that the endpoint was still alive. But VPC endpoints in AWS have a state machine: `PendingAcceptance` → `Available` → `Deleting` → `Deleted`. An endpoint in `Deleting` or `Deleted` state still appears in API responses — it just hasn't been garbage-collected yet.

The reconciler's logic was:

1. Call `DeleteVpcEndpoints` for the endpoint.
2. Call `DescribeVpcEndpoints` to check if it's gone.
3. If the endpoint is present in the response, back off and retry.
4. Only proceed to security group cleanup after the endpoint is fully absent.

Steps 2-3 created a retry loop that waited for AWS to fully garbage-collect the endpoint — which can take minutes. During this backoff window, the security group remained in use by the "deleting" endpoint, so any attempt to delete the security group also failed. The compounding backoff meant that by the time the endpoint was finally garbage-collected, the reconciler had backed off so far that the next attempt would take minutes more. In testing, this affected approximately 40% of ROSA HCP cluster deletions.

[@sdminonne](https://github.com/sdminonne)'s [PR #9517](https://github.com/openshift/hypershift/pull/9517) fixed the logic to check the endpoint's `State` field instead of its mere presence. Endpoints in `Deleting` or `Deleted` states are treated as effectively removed, and the reconciler proceeds immediately to security group cleanup. The security group deletion might still fail temporarily (because the endpoint is still `Deleting`), but that's handled by the normal retry loop at the security group level — a much shorter retry cycle than the endpoint-level backoff.

The fix is two lines of meaningful logic change (check state instead of presence), but its impact was enormous: it unblocked VPC deletion for BYO-VPC customers whose VPCs were accumulating orphaned security groups from every failed cluster teardown. Each orphaned security group counts against the VPC security group quota, so long-lived VPCs could eventually hit the limit and be unable to create new clusters.

!!! warning "Backport Status"
    This fix was cherry-picked to release-5.0 ([PR #9576](https://github.com/openshift/hypershift/pull/9576)) and requires backports to release-4.19 through release-5.2 to cover all affected versions.

---

## :material-swap-horizontal: Karpenter Drift Detection: The Premature Node Replacement Bug

Karpenter's drift detection is designed to replace worker nodes when the underlying machine image or configuration changes — for example, when a new AMI is published or userData is updated. In HyperShift, drift detection triggers when the control plane upgrade changes the target AMI or ignition config. But the detection was firing too early, before the control plane upgrade actually completed.

Here's the race condition: when a control plane upgrade starts, `hcp.Spec.ReleaseImage` is updated to the new version immediately. The EC2NodeClass controller reads this field to determine the target AMI and userData. It updates the EC2NodeClass with the new values, which Karpenter's drift detector sees as a configuration change. Karpenter starts replacing nodes — but the control plane hasn't finished rolling out yet. The new nodes try to join a cluster whose API server, controller manager, and scheduler are still running the old version. Depending on the version gap, this can cause join failures, kubelet version skew issues, or at minimum unnecessary disruption.

[@maxcao13](https://github.com/maxcao13)'s [PR #9234](https://github.com/openshift/hypershift/pull/9234) fixed this by replacing `hcp.Spec.ReleaseImage` with the *most recently completed* release from `HostedControlPlane.Status.VersionHistory`. The EC2NodeClass controller now only updates `userData` and `AMISelectorTerms` when the control plane has fully rolled out to the target version — meaning all deployments are running the new version and the rollout is marked `Completed` in the version history.

This PR was itself a second attempt. The first implementation ([PR #8957](https://github.com/openshift/hypershift/pull/8957)) used the same approach but caused CI flakes and was reverted in [PR #9233](https://github.com/openshift/hypershift/pull/9233). The second attempt added safeguards: explicit handling of the initial rollout case (where there's no completed version yet), better guards against empty version history during cluster bootstrap, and expanded unit tests covering upgrade rollout completion, node stability during upgrades, and the interaction between drift detection and release history.

The practical impact was significant for ROSA HCP customers using Karpenter/AutoNode. Before the fix, every control plane upgrade triggered a premature replacement of all unpinned worker nodes, causing workload disruption during what should have been a control-plane-only operation. After the fix, worker node replacement waits until the control plane is fully ready to serve the new version.

!!! tip "Design Pattern"
    The general lesson here is: don't gate worker-node behavior on `Spec` fields that change optimistically. Use `Status` fields that reflect *completed* state. This is the same principle behind Kubernetes Deployments using `.Status.ReadyReplicas` rather than `.Spec.Replicas` to determine rollout completion.

---

## :material-star-shooting: Beneath the Headlines

!!! info "276 PRs, 5 stories — what about the rest?"
    The stories above cover fewer than 15 of the 276 merged PRs. Here is a sampling of the other significant work that landed during this period.

**AWS reserved tags breaking cluster upgrades.** [@reedcort](https://github.com/reedcort)'s [PR #9538](https://github.com/openshift/hypershift/pull/9538) fixed a bug where AWS-injected tags with the `aws:` prefix were passed to `ec2.DeleteTags`, which AWS rejects with an error. The reconciler would retry indefinitely, blocking all cluster upgrades for affected HostedClusters. The fix filters `aws:`-prefixed keys from the remove map before calling DeleteTags. Simple, but it was blocking upgrades across release-4.19 through release-5.2 until a manual annotation workaround was applied.

**Status patching migration.** [@vsolanki12](https://github.com/vsolanki12) continued the systematic migration of HostedControlPlane status writes to the shared `statuspatching` library. [PR #8966](https://github.com/openshift/hypershift/pull/8966) migrated nine CPO call sites with stale-generation guards and re-fetch-before-write patterns. [PR #9385](https://github.com/openshift/hypershift/pull/9385) completed the last two sites including a CPO route-ingress patch that previously had *no optimistic lock at all*. [PR #9388](https://github.com/openshift/hypershift/pull/9388) introduced the `hcpstatuspatch` linter analyzer (initially disabled), and [@cblecker](https://github.com/cblecker)'s [PR #9563](https://github.com/openshift/hypershift/pull/9563) extended it to cover HostedCluster writes and enabled it in standard lint runs with temporary exceptions for unmigrated call sites.

**NodePool AWSMachineTemplate hash flip.** [@muraee](https://github.com/muraee)'s [PR #9456](https://github.com/openshift/hypershift/pull/9456) fixed a bug where the `CreateDefaultAWSSecurityGroup` capability flag — derived fresh on every reconcile from the release payload's image labels — could flip between `true` and `false` during a brief window after cluster creation. When it flipped, the AWSMachineTemplate hash changed, triggering a full unrequested worker node replacement. The fix gates default security group injection on `status.platform.aws.defaultWorkerSecurityGroupID` instead of the transient capability flag, ensuring the hash is stable once the security group ID is populated.

**AWS identity provider misclassification.** [@sdminonne](https://github.com/sdminonne)'s [PR #9406](https://github.com/openshift/hypershift/pull/9406) found and fixed a dead code path in the AWS health check. After the AWS SDK v2 migration, the `apiErr.ErrorCode() == "WebIdentityErr"` check never matched because the SDK v2 wraps STS errors differently. The result: *any* STS error — including transient I/O pressure, missing token files, or infrastructure hiccups — was classified as "invalid identity provider," which triggered Limited Support notifications to customers. The fix uses `errors.As` with `smithy.APIError` to inspect the actual error chain and map specific codes (`AccessDenied`, `ExpiredTokenException`, `IDPRejectedClaim`) to the "invalid" condition while treating everything else as `Unknown` (transient).

**KubeVirt additionalNetworks CEL validation.** [@chdeshpa-hue](https://github.com/chdeshpa-hue)'s [PR #8710](https://github.com/openshift/hypershift/pull/8710) added admission-time validation for KubeVirt NodePool `additionalNetworks[].name` using CEL regex rules and `+listType=map`/`+listMapKey=name` schema markers. Previously, invalid network names (missing namespace prefix, overlong, or duplicate entries) were silently accepted at the API level but caused VM creation failures deep in the hosted control plane namespace — a frustrating debugging experience. The CEL regex enforces `<namespace>/<name>` DNS-label format, and `maxLength` was reduced from 255 to 55 to respect KubeVirt's internal interface name limit.

**Post-quantum cryptography base images.** [@yiraeChristineKim](https://github.com/yiraeChristineKim)'s PRs [#9452](https://github.com/openshift/hypershift/pull/9452) and [#9453](https://github.com/openshift/hypershift/pull/9453) switched the hypershift-operator's runtime base image from `ubi9/ubi-minimal` to the PQC-enabled `ubi9/ubi-minimal-pqc`, enabling `DEFAULT:PQ` post-quantum cryptographic algorithm support. [PR #9463](https://github.com/openshift/hypershift/pull/9463) did the same for the CLI image, using a COPY overlay from the PQC base image since no PQC-enabled `nginx-124` image exists yet. This is part of a broader OpenShift initiative (OCPSTRAT-3113) to prepare the platform for post-quantum cryptographic requirements.

**GCP Persistent Disk CSI driver wiring.** [@ckandag](https://github.com/ckandag)'s [PR #9450](https://github.com/openshift/hypershift/pull/9450) completed the HyperShift-side integration for the GCP PD CSI driver — cloud config ConfigMap, credential Secrets with WIF support, self-signed metrics serving certificates (needed because GCP lacks service-ca-operator), and CVO resource exclusion to prevent conflicts. A subtle detail: without the explicit cloud-config, the GCE instance metadata server resolves to the *management cluster's* project instead of the tenant's, causing cross-project volume operations to fail silently.

**OpenTelemetry distributed tracing.** [@dustman9000](https://github.com/dustman9000)'s [PR #9390](https://github.com/openshift/hypershift/pull/9390) added opt-in OTEL SDK-based tracing to the HyperShift operator for HostedCluster and NodePool reconciliation. Root and per-phase child spans are created with attributes and error recording, and W3C `traceparent` annotations are extracted from HostedCluster objects to link spans across OCM Cluster Service and the HyperShift operator. When the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable is unset, the tracer is a no-op with zero overhead. The 38 new unit tests cover provider initialization, span creation, error recording, and link extraction.

**Force destroy scoping fix.** [@bryan-cox](https://github.com/bryan-cox)'s [PR #9398](https://github.com/openshift/hypershift/pull/9398) fixed `hypershift destroy cluster --force` to scope finalizer removal to NodePools belonging to the target HostedCluster only. Previously, it stripped finalizers from *all* NodePools in the shared namespace — discovered when an Azure deprovisioner rehearsal processed 186 NodePools and accidentally modified ones belonging to other HostedClusters. The regression test creates a NodePool owned by a different HostedCluster and verifies its finalizer survives the force-destroy operation.

**Ignition server TLS hardening.** [@ingvagabund](https://github.com/ingvagabund)'s [PR #8910](https://github.com/openshift/hypershift/pull/8910) closed a security gap where the ignition server and its HAProxy proxy didn't honor the global TLS security profile. This meant that even when the cluster was configured for TLS 1.3 minimum, the ignition server would happily accept TLS 1.0 connections. The fix adds CLI options for minimum TLS version and cipher suites, renders a managed HAProxy configuration with the correct TLS profile, and hardens the proxy container to run as non-root with read-only filesystem.

---

## :material-chart-bar: By the Numbers

| Metric | Value |
|--------|-------|
| Total PRs merged | 276 |
| Unique contributors | 71 |
| Bot PRs | 53 |
| hypershift PRs | 189 |
| ai-helpers PRs | 1 |
| enhancements PRs | 2 |
| release PRs | 84 |
| Average merge time | 338.4 hours |
| High-impact PRs | 0 |
| Breaking changes | 0 |
| API changes | 0 |
| Customer-reported fixes | 10 |

**Top Reviewers**

| Reviewer | PRs Reviewed |
|----------|-------------|
| [@bryan-cox](https://github.com/bryan-cox) | 70 |
| [@jparrill](https://github.com/jparrill) | 36 |
| [@csrwng](https://github.com/csrwng) | 26 |
| [@cblecker](https://github.com/cblecker) | 24 |
| [@ironcladlou](https://github.com/ironcladlou) | 23 |

---

## :octicons-people-24: Contributors

Click any column header to sort. Each number links to the contributor's PRs in that repository.

| Contributor | hypershift | ai-helpers | enhancements | release | :material-bug: bugs | Total |
|------------|:-: | :-: | :-: | :-:|:-:|:-:|
| [@bryan-cox](https://github.com/bryan-cox) | [21](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-08-23..2026-09-22) |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-08-23..2026-09-22+hypershift) | [10](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **24** |
| [@ironcladlou](https://github.com/ironcladlou) | [16](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-08-23..2026-09-22) |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-08-23..2026-09-22+hypershift) | [7](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aironcladlou+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **19** |
| [@jparrill](https://github.com/jparrill) | [17](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-08-23..2026-09-22) |  |  | [1](https://github.com/openshift/release/pull/84848) | [8](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **18** |
| [@mgencur](https://github.com/mgencur) | [6](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-08-23..2026-09-22) |  |  | [8](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-08-23..2026-09-22+hypershift) | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **14** |
| [@vsolanki12](https://github.com/vsolanki12) | [10](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-08-23..2026-09-22) |  |  |  | [7](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **10** |
| [@yiraeChristineKim](https://github.com/yiraeChristineKim) | [8](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3AyiraeChristineKim+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9558) | **8** |
| [@rutvik23](https://github.com/rutvik23) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Arutvik23+merged%3A2026-08-23..2026-09-22) |  | [1](https://github.com/openshift/enhancements/pull/2038) | [1](https://github.com/openshift/release/pull/84480) | [1](https://github.com/openshift/hypershift/pull/8787) | **7** |
| [@cblecker](https://github.com/cblecker) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acblecker+merged%3A2026-08-23..2026-09-22) |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Acblecker+merged%3A2026-08-23..2026-09-22+hypershift) |  | **6** |
| [@dhgautam99](https://github.com/dhgautam99) | [6](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adhgautam99+merged%3A2026-08-23..2026-09-22) |  |  |  | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Adhgautam99+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **6** |
| [@PoornimaSingour](https://github.com/PoornimaSingour) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-08-23..2026-09-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **5** |
| [@clebs](https://github.com/clebs) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aclebs+merged%3A2026-08-23..2026-09-22) |  |  |  | [3](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Aclebs+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **5** |
| [@jimdaga](https://github.com/jimdaga) | [1](https://github.com/openshift/hypershift/pull/9436) |  |  | [4](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ajimdaga+merged%3A2026-08-23..2026-09-22+hypershift) |  | **5** |
| [@jmguzik](https://github.com/jmguzik) |  |  |  | [4](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ajmguzik+merged%3A2026-08-23..2026-09-22+hypershift) |  | **4** |
| [@michaelryanmcneill](https://github.com/michaelryanmcneill) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amichaelryanmcneill+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9425) | **4** |
| [@sdminonne](https://github.com/sdminonne) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Asdminonne+merged%3A2026-08-23..2026-09-22) |  |  |  | [4](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Asdminonne+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **4** |
| [@ckandag](https://github.com/ckandag) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ackandag+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9598) | **3** |
| [@dustman9000](https://github.com/dustman9000) | [1](https://github.com/openshift/hypershift/pull/9390) |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Adustman9000+merged%3A2026-08-23..2026-09-22+hypershift) |  | **3** |
| [@enxebre](https://github.com/enxebre) |  | [1](https://github.com/openshift-eng/ai-helpers/pull/747) |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-08-23..2026-09-22+hypershift) |  | **3** |
| [@gbarabasz](https://github.com/gbarabasz) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Agbarabasz+merged%3A2026-08-23..2026-09-22) |  |  | [1](https://github.com/openshift/release/pull/84310) |  | **3** |
| [@hlipsig](https://github.com/hlipsig) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ahlipsig+merged%3A2026-08-23..2026-09-22) |  |  |  |  | **3** |
| [@maxcao13](https://github.com/maxcao13) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amaxcao13+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9234) | **3** |
| [@mehabhalodiya](https://github.com/mehabhalodiya) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amehabhalodiya+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9279) | **3** |
| [@openshift-ci](https://github.com/openshift-ci) |  |  |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aopenshift-ci+merged%3A2026-08-23..2026-09-22+hypershift) |  | **3** |
| [@patjlm](https://github.com/patjlm) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Apatjlm+merged%3A2026-08-23..2026-09-22) |  |  | [1](https://github.com/openshift/release/pull/84397) |  | **3** |
| [@daniel-rejniak](https://github.com/daniel-rejniak) | [1](https://github.com/openshift/hypershift/pull/9466) |  |  | [1](https://github.com/openshift/release/pull/85479) |  | **2** |
| [@deads2k](https://github.com/deads2k) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adeads2k+merged%3A2026-08-23..2026-09-22) |  |  |  |  | **2** |
| [@deepsm007](https://github.com/deepsm007) |  |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Adeepsm007+merged%3A2026-08-23..2026-09-22+hypershift) |  | **2** |
| [@hector-vido](https://github.com/hector-vido) |  |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ahector-vido+merged%3A2026-08-23..2026-09-22+hypershift) |  | **2** |
| [@ingvagabund](https://github.com/ingvagabund) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aingvagabund+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8910) | **2** |
| [@jatinsu](https://github.com/jatinsu) |  |  |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ajatinsu+merged%3A2026-08-23..2026-09-22+hypershift) |  | **2** |
| [@joshbranham](https://github.com/joshbranham) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajoshbranham+merged%3A2026-08-23..2026-09-22) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9615) | **2** |
| [@qinqon](https://github.com/qinqon) | [1](https://github.com/openshift/hypershift/pull/9514) |  |  | [1](https://github.com/openshift/release/pull/83031) | [1](https://github.com/openshift/release/pull/83031) | **2** |
| [@tony-schndr](https://github.com/tony-schndr) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Atony-schndr+merged%3A2026-08-23..2026-09-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Atony-schndr+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **2** |
| [@vismishr](https://github.com/vismishr) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avismishr+merged%3A2026-08-23..2026-09-22) |  |  |  | [2](https://github.com/search?q=org%3Aopenshift+is%3Apr+is%3Amerged+author%3Avismishr+merged%3A2026-08-23..2026-09-22+OCPBUGS&type=pullrequests) | **2** |
| [@JoelSpeed](https://github.com/JoelSpeed) | [1](https://github.com/openshift/hypershift/pull/9371) |  |  |  |  | **1** |
| [@Nikokolas3270](https://github.com/Nikokolas3270) | [1](https://github.com/openshift/hypershift/pull/8882) |  |  |  |  | **1** |
| [@Nirshal](https://github.com/Nirshal) | [1](https://github.com/openshift/hypershift/pull/8584) |  |  |  |  | **1** |
| [@RamLavi](https://github.com/RamLavi) | [1](https://github.com/openshift/hypershift/pull/9286) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9286) | **1** |
| [@acwalczyk](https://github.com/acwalczyk) | [1](https://github.com/openshift/hypershift/pull/9472) |  |  |  |  | **1** |
| [@amisstea](https://github.com/amisstea) |  |  |  | [1](https://github.com/openshift/release/pull/84500) |  | **1** |
| [@apahim](https://github.com/apahim) | [1](https://github.com/openshift/hypershift/pull/9306) |  |  |  |  | **1** |
| [@avollmer-redhat](https://github.com/avollmer-redhat) | [1](https://github.com/openshift/hypershift/pull/8880) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8880) | **1** |
| [@bennerv](https://github.com/bennerv) | [1](https://github.com/openshift/hypershift/pull/9506) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9506) | **1** |
| [@bharath-b-rh](https://github.com/bharath-b-rh) |  |  |  | [1](https://github.com/openshift/release/pull/83309) |  | **1** |
| [@bmeng](https://github.com/bmeng) |  |  |  | [1](https://github.com/openshift/release/pull/84784) |  | **1** |
| [@celebdor](https://github.com/celebdor) | [1](https://github.com/openshift/hypershift/pull/9482) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9482) | **1** |
| [@chdeshpa-hue](https://github.com/chdeshpa-hue) | [1](https://github.com/openshift/hypershift/pull/8710) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8710) | **1** |
| [@cristianoveiga](https://github.com/cristianoveiga) |  |  |  | [1](https://github.com/openshift/release/pull/84832) |  | **1** |
| [@cssjr](https://github.com/cssjr) | [1](https://github.com/openshift/hypershift/pull/9403) |  |  |  |  | **1** |
| [@devguyio](https://github.com/devguyio) |  |  | [1](https://github.com/openshift/enhancements/pull/2039) |  |  | **1** |
| [@dorzel](https://github.com/dorzel) |  |  |  | [1](https://github.com/openshift/release/pull/81572) |  | **1** |
| [@georgelipceanu](https://github.com/georgelipceanu) | [1](https://github.com/openshift/hypershift/pull/8926) |  |  |  |  | **1** |
| [@jhjaggars](https://github.com/jhjaggars) | [1](https://github.com/openshift/hypershift/pull/8681) |  |  |  |  | **1** |
| [@joelsmith](https://github.com/joelsmith) |  |  |  | [1](https://github.com/openshift/release/pull/84052) |  | **1** |
| [@jsafrane](https://github.com/jsafrane) |  |  |  | [1](https://github.com/openshift/release/pull/83273) |  | **1** |
| [@kaleemsiddiqu](https://github.com/kaleemsiddiqu) | [1](https://github.com/openshift/hypershift/pull/8886) |  |  |  |  | **1** |
| [@ketanvyas21](https://github.com/ketanvyas21) | [1](https://github.com/openshift/hypershift/pull/8985) |  |  |  | [1](https://github.com/openshift/hypershift/pull/8985) | **1** |
| [@linkvt](https://github.com/linkvt) | [1](https://github.com/openshift/hypershift/pull/9565) |  |  |  |  | **1** |
| [@matlaj](https://github.com/matlaj) | [1](https://github.com/openshift/hypershift/pull/9694) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9694) | **1** |
| [@mcornea](https://github.com/mcornea) |  |  |  | [1](https://github.com/openshift/release/pull/85096) |  | **1** |
| [@memodi](https://github.com/memodi) |  |  |  | [1](https://github.com/openshift/release/pull/83541) |  | **1** |
| [@muraee](https://github.com/muraee) | [1](https://github.com/openshift/hypershift/pull/9456) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9456) | **1** |
| [@orenc1](https://github.com/orenc1) | [1](https://github.com/openshift/hypershift/pull/7431) |  |  |  | [1](https://github.com/openshift/hypershift/pull/7431) | **1** |
| [@petr-muller](https://github.com/petr-muller) |  |  |  | [1](https://github.com/openshift/release/pull/84124) |  | **1** |
| [@rbhilare](https://github.com/rbhilare) |  |  |  | [1](https://github.com/openshift/release/pull/83489) |  | **1** |
| [@reedcort](https://github.com/reedcort) | [1](https://github.com/openshift/hypershift/pull/9538) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9538) | **1** |
| [@roivaz](https://github.com/roivaz) |  |  |  | [1](https://github.com/openshift/release/pull/85192) |  | **1** |
| [@shannon](https://github.com/shannon) | [1](https://github.com/openshift/hypershift/pull/9023) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9023) | **1** |
| [@stbenjam](https://github.com/stbenjam) |  |  |  | [1](https://github.com/openshift/release/pull/84626) |  | **1** |
| [@stephenfin](https://github.com/stephenfin) | [1](https://github.com/openshift/hypershift/pull/9467) |  |  |  | [1](https://github.com/openshift/hypershift/pull/9467) | **1** |
| [@thetechnick](https://github.com/thetechnick) | [1](https://github.com/openshift/hypershift/pull/8884) |  |  |  |  | **1** |

---

## :material-crystal-ball: What's Next

The reliability focus of this period sets up several important threads for the coming months.

**Konnectivity hardening validation.** The `--sync-forever` flag, multi-IP DNS fallback, and certificate splitting are deployed. The next step is long-duration soak testing under controlled failure injection (node drains, load balancer failovers, certificate rotation) to validate that tunnel recovery is consistent across all failure modes and timing windows.

**Status patching completion.** The `hcpstatuspatch` linter is now enforced with temporary exceptions for 25 unmigrated call sites. Each exception is tracked as migration debt. The goal is to close all exceptions over the next 1-2 cycles, at which point the linter becomes a zero-tolerance gate for unsafe status writes.

**VPC endpoint and security group cleanup at scale.** The state machine fix for VPC endpoints addresses the immediate deletion failure, but the broader question of orphaned AWS resource cleanup — security groups, ENIs, and VPC endpoints — remains an active area of work. Automated resource reconciliation and quota monitoring are being explored for fleet-wide deployment.

**CAPI v1beta2 migration rollout.** The IPAM CRD exclusion fix ([PR #9642](https://github.com/openshift/hypershift/pull/9642)) unblocked CVO upgrades on ROSA HCP management clusters. With that resolved, the CAPI storage version migration can proceed with controlled rollout, monitoring for edge cases in CRDs with unusual managed field histories.

**GCP platform maturation.** GCP PD CSI wiring is complete and backported. The GCP OrphanDeleter for WIF credentials landed this month. Next up: GCP PSC NAT subnet discovery optimizations are merged, and the platform is approaching feature parity for hosted cluster lifecycle operations.

The pace continues.
