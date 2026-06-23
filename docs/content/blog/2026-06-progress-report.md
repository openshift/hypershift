---
title: June 2026 Progress Report
description: CAPI v1.11, dual-stream RHEL, production reliability fixes, etcd re-encryption, CI improvements, and agentic automation — 297 PRs from 43 contributors.
---

# June 2026 Progress Report

<div class="grid cards" markdown>

-   :octicons-git-pull-request-24:{ .lg } **297** PRs merged
-   :octicons-people-24:{ .lg } **43** contributors
-   :octicons-alert-24:{ .lg } **18** breaking changes
-   :octicons-clock-24:{ .lg } **213.6h** avg merge time

</div>

If you follow HyperShift development, you know the project occupies a peculiar position in the Kubernetes ecosystem: it is simultaneously middleware, a platform, and an opinion about how control planes should work. It hosts OpenShift control planes as pods on a management cluster while workers run in separate infrastructure — a model that powers both ROSA HCP and ARO HCP at scale for Red Hat's managed services. What you might not appreciate from the outside is the sheer velocity required to keep that model working as the ground shifts underneath it: new Kubernetes releases, new OS versions, new cloud provider requirements, and the constant drumbeat of production incidents demanding immediate attention.

Over the six weeks from May 14 to June 22, 2026, the HyperShift project merged 297 pull requests across the hypershift, release, enhancements, and ai-helpers repositories. Forty-three contributors participated, plus bots that contributed 22 PRs to hypershift (cherry-picks, dependency updates, automated Jira fixes) and 4 to release. The average time from PR open to merge was 213.6 hours — about 8.9 days — reflecting the project's thorough review culture where critical changes routinely go through multiple rounds of CI validation. Thirty-one of those PRs were classified as high impact, 18 introduced breaking changes, and 11 modified the API surface. The themes that emerged were unmistakable: preparing for OCP 5.0's dual-stream OS future, hardening production reliability after real incidents, and investing in automation — both in CI infrastructure and in the emerging practice of AI-assisted development workflows.

What follows are seven stories from this period, chosen not because they were the largest diffs but because they best illustrate the technical challenges and engineering judgment that define this project.

---

## :material-swap-horizontal: The CAPI 1.11 Migration — 600 Files and a Storage Version Override

The Cluster API (CAPI) project is the Kubernetes ecosystem's standard interface for declarative infrastructure management, and HyperShift depends on it deeply. When CAPI 1.11 shipped with v1beta2 CRDs, it was not a question of whether HyperShift would adopt it but how — and that "how" turned out to be the interesting part.

The naive approach to a CAPI version bump is straightforward: update the imports, regenerate the CRDs, fix the compilation errors. The reality of [PR #8594](https://github.com/openshift/hypershift/pull/8594) by [@enxebre](https://github.com/enxebre) was nothing like that. CAPI 1.11 introduces v1beta2 as a new served version alongside v1beta1, but HyperShift cannot simply flip the storage version to v1beta2 on a live management cluster hosting hundreds of active HostedClusters. Doing so would require a storage version migration of every CAPI object in etcd — Machines, MachineDeployments, MachineSets, Clusters — and a single failure during that migration could leave the cluster in an inconsistent state.

The solution was surgical: override the storage version back to v1beta1 using `CAPICRDOverrides`, install both versions as served, and disable conversion webhooks by default via a new `--disable-capi-conversion-webhook` flag. This means existing objects continue to be stored as v1beta1, new clients can speak v1beta2, and no migration is needed at the moment of upgrade. The conversion webhooks remain available but off by default until the team is ready for a controlled migration.

The PR touched 600 files. Eighty Go files needed import path updates from the CAPI module reorganization. Thirty YAML files contained CRD manifests that needed regeneration. The remaining 486 files were vendored dependencies — the entire CAPI provider chain (AWS, Azure, IBM Cloud, KubeVirt, Agent, OpenStack, PowerVS) had to be bumped to v1.11-compatible versions simultaneously. The builder image was updated to golang-1.25 to satisfy the new dependency chain.

One subtle but important detail: this PR also removed HyperShift's dependency on a downstream CAPI fork. Previous versions had carried patches that were now upstreamed, so the migration doubled as a cleanup of technical debt. The test coverage included a new `TestCRDIncludeFilter` with 17+ cases validating the CRD filtering logic and `TestSetupCAPIConversionWebhook` for the webhook configuration.

!!! tip "Key Takeaway"
    This is the kind of PR that does not get written about in release notes but determines whether the next six months of development are painful or productive. It is a prerequisite for the Kubernetes 1.33 upgrade, and it positions HyperShift to adopt v1beta2 fully when the ecosystem is ready.

---

## :material-linux: Dual-Stream RHEL — Teaching HyperShift to Speak Two Languages

OpenShift 5.0 marks a generational transition: the operating system layer moves from a single RHCOS stream built on RHEL 9 to a dual-stream model supporting both RHEL 9 and RHEL 10. For standalone OpenShift, this is managed through MachineConfigPools. For HyperShift, where the NodePool is the analog of a MachineConfigPool, the question is: how do you let different NodePools in the same HostedCluster run different OS versions?

The answer required coordinated work across five PRs and an enhancement proposal, driven by [@jparrill](https://github.com/jparrill), [@sdminonne](https://github.com/sdminonne), and [@celebdor](https://github.com/celebdor) (with the enhancement by [@enxebre](https://github.com/enxebre)).

The foundation was laid in [PR #8673](https://github.com/openshift/hypershift/pull/8673) by [@sdminonne](https://github.com/sdminonne), which ripped out approximately 114 lines of hand-rolled CoreOS stream metadata types that had been living in HyperShift since August 2021. These custom types — `CoreOSStreamMetadata`, `CoreOSArchitecture`, `CoreOSImages` — were written when HyperShift was AWS-only and predated the canonical upstream library `coreos/stream-metadata-go`. The upstream library, already used by the OpenShift installer since v0.4.10, covers every platform type HyperShift supports. Adopting it meant updating every platform-specific boot image resolver (AWS, Azure, GCP, KubeVirt, PowerVS, OpenStack) to use the upstream field names (notably `Aws` instead of `AWS`), adding nil checks for pointer-typed fields, and updating 27 Go files across 7 platforms.

With the upstream types in place, [PR #8669](https://github.com/openshift/hypershift/pull/8669) by [@jparrill](https://github.com/jparrill) added the multi-stream parsing logic. The key function, `DeserializeImageMetadata`, had to be updated to handle both the legacy single-stream `coreos-bootimages` ConfigMap format (a single `stream` key) and the new multi-stream format (a `streams` key containing a map of stream names to stream metadata). The PR introduced `StreamForName()` with a fallback path and `GetRHELStream()` for resolution logic, backed by 20 tests for stream resolution, 11 for deserialization, and 12 for name lookup. A 1,047-line test fixture was extracted from a real OCP 5.0 payload.

[PR #8675](https://github.com/openshift/hypershift/pull/8675) by [@sdminonne](https://github.com/sdminonne) then added the API surface: a new `osImageStream` field on both `NodePoolSpec` and `NodePoolStatus`. The type uses an `OSImageStreamReference` enum (`rhel-9` / `rhel-10`), matching the naming convention used by the installer's `types.OSImageStream` and the MCO's `io.openshift.os.streamclass` label. CEL validation prevents downgrade from RHEL 10 back to RHEL 9 — a one-way door, because RHEL 10 removes runc entirely. The whole thing is gated behind the `OSStreams` TechPreview feature gate, with 236 lines of envtest CEL tests validating the constraint.

[PR #8699](https://github.com/openshift/hypershift/pull/8699) by [@jparrill](https://github.com/jparrill) decoupled the AWS AMI resolution to be stream-aware, threading the stream selection through `machineTemplateBuilders()` in `capi.go`. And [PR #8719](https://github.com/openshift/hypershift/pull/8719) by [@sdminonne](https://github.com/sdminonne) added a CEL rule preventing removal of `osImageStream` once set, with the `ValidOSImageStream` condition for validation feedback.

The [enhancement proposal](https://github.com/openshift/enhancements/pull/2019) itself (enhancements #2019 by [@enxebre](https://github.com/enxebre)) is a 727-line document that lays out the full vision: how stream selection threads through the token secret, ignition server, and MCO bootstrap pipeline; the runc upgrade guard for RHEL 10; and the constraint that OpenShift 5.0 defaults to RHEL 10 while existing clusters remain on RHEL 9. This is not just an API field — it is a complete rethinking of how the OS layer interacts with the NodePool lifecycle.

---

## :material-lan-disconnect: The NLB Blackout — When Load Balancers Go Dark

!!! danger "Production Impact"
    Management cluster KAS rollouts caused 20–30 minute API blackouts for hosted clusters using private NLB routing.

Some bugs announce themselves with a crash. Others announce themselves with silence — the kind where your monitoring dashboards go flat and your on-call engineer's phone lights up with "API unreachable" alerts from hosted clusters using private NLB routing.

The Jira issue (OCPBUGS-87018) describes it with the clinical precision of a postmortem: "MC kube-apiserver revision rollouts cause persistent NLB traffic blackouts for all hosted cluster APIs on HyperShift management clusters." The blackouts typically lasted 20 to 30 minutes and affected hosted clusters with private or public-and-private endpoint access on the affected management cluster.

The root cause was an OVN-Kubernetes bug ([OCPBUGS-87020](https://issues.redhat.com/browse/OCPBUGS-87020)): during a management cluster KAS (kube-apiserver) revision rollout, `ovnkube-controller` loses its API connection and re-syncs. During this re-sync, it writes OVN NorthDB logical flows that reference port groups before those port groups are fully restored — a non-atomic operation. `ovn-controller` silently drops the malformed flows, and without the allow ACL, the namespace default-deny drops all NLB traffic to HCP router pods. Since `resyncInterval=0`, OVN would not rewrite the flows on its own — the stale state persisted until some unrelated event (a NetworkPolicy update, a pod change) happened to trigger OVN to reprocess the affected flows, which typically took 20–30 minutes on a busy management cluster.

What made this particularly insidious in HyperShift was the interaction with NetworkPolicy churn. The HCP namespace egress policies (`private-router`, `management-kas`) use `/32` CIDRs derived from the management cluster's KAS endpoint IPs. These IPs rotate during KAS rolling restarts, causing a burst of NetworkPolicy updates that amplified the OVN race window.

The response came in three stages. First, [PR #8662](https://github.com/openshift/hypershift/pull/8662) by [@csrwng](https://github.com/csrwng) applied an emergency workaround: revert the conditional `openshift-ingress` NetworkPolicy deletion (from PR #7872) to reduce OVN churn during re-sync. Then, [PR #8689](https://github.com/openshift/hypershift/pull/8689) by [@Ajpantuso](https://github.com/Ajpantuso) delivered the proper fix: a new `--hcp-egress-block-cidrs` flag that lets operators supply stable CIDR blocks (e.g., the MC machine network) instead of dynamically discovered KAS endpoint IPs. This eliminates the NetworkPolicy churn entirely, removing the trigger for the OVN race. With the real fix in place, [PR #8754](https://github.com/openshift/hypershift/pull/8754) by [@csrwng](https://github.com/csrwng) re-reverted #8662 to restore the original conditional NetworkPolicy behavior — the blanket workaround was no longer needed.

The underlying OVN bug (non-atomic port group writes during incremental processing) is being tracked upstream as [FDP-3965](https://issues.redhat.com/browse/FDP-3965).

!!! warning "Lesson Learned"
    In multi-tenant control plane infrastructure, the blast radius of upstream networking bugs is amplified by the density of NetworkPolicy objects across hundreds of HCP namespaces. The fix required understanding the interaction across three layers — OVN internals, NetworkPolicy reconciliation patterns, and NLB health checking — and the team shipped three PRs in sequence: emergency workaround, proper fix, cleanup.

---

## :material-memory: The Metrics-Proxy Memory Leak — 3 Lines, 2700 MiB

!!! danger "Production Impact"
    Memory growing from 40 MiB to 2,774 MiB on request-serving nodes — a 69x increase squeezing out hosted cluster workloads.

The best bugs are the ones where the diff is inversely proportional to the impact. [PR #8740](https://github.com/openshift/hypershift/pull/8740) by [@muraee](https://github.com/muraee) is a masterclass in this genre.

The metrics-proxy deployment on ROSA HCP request-serving nodes was growing from its initial 40 MiB memory footprint to 2,774 MiB — a 69x increase. On request-serving nodes, which host control plane pods for multiple HostedClusters, this memory growth was squeezing out actual workloads. The bug was filed as OCPBUGS-88685 with Critical priority.

The initial investigation went down the expected paths: was the proxy caching too much data? Was there a goroutine leak? The actual root cause was more subtle and more embarrassing. The kube-rbac-proxy sidecar, which fronts the metrics endpoint, was configured with verbose logging enabled. At high verbosity, kube-rbac-proxy logs the full request and response bodies. For a metrics proxy, the response bodies are Prometheus scrape payloads — potentially megabytes of time-series data per scrape. Every 30 seconds, every scrape, the full payload was being serialized into the log buffer and held in memory.

But wait — the real kicker was in the HTTP transport layer. The metrics-proxy scraper was not closing idle HTTP connections. Each scrape opened a new connection, and because `DisableKeepAlives` was not set on the `http.Transport`, the connections accumulated. Combined with the verbose logging retaining references to response bodies, the garbage collector could never reclaim the memory.

The fix was three lines of production code:

1. Set `DisableKeepAlives: true` on the `http.Transport`
2. Add `defer transport.CloseIdleConnections()` after each scrape
3. Set `--v=0` on the kube-rbac-proxy sidecar to disable verbose logging

The PR included 184 lines of new tests with 8 test cases, including a specific connection leak regression test. The bug had been silently accumulating in production, never quite triggering OOM kills (the containers had generous limits) but steadily consuming node resources that should have been available for hosted cluster workloads.

---

## :material-lock-reset: Etcd Re-encryption — Automating Key Rotation for Compliance

When you rotate an encryption key, you have a new key and all the old data is still encrypted with the old key. If an attacker compromises the old key after rotation, they can still read everything that was encrypted before the rotation. The solution is re-encryption: after rotating the key, you must read every encrypted resource from etcd, decrypt it with the old key, re-encrypt it with the new key, and write it back. For ARO-HCP clusters, this is not optional — it is a compliance requirement.

Before this work by [@muraee](https://github.com/muraee), re-encryption was a manual process. An operator would rotate the encryption key, then manually trigger a storage migration to re-encrypt all resources. This is error-prone, easy to forget, and impossible to audit automatically.

The [enhancement proposal](https://github.com/openshift/enhancements/pull/1969) (enhancements #1969) laid out a comprehensive design: a new HCCO (Hosted Cluster Config Operator) re-encryption controller that watches for encryption key changes, creates `StorageVersionMigration` CRs in the guest cluster to trigger re-encryption of all encrypted resource types, and reports progress through a new `EtcdDataEncryptionUpToDate` condition on the HCP and HostedCluster.

[PR #8219](https://github.com/openshift/hypershift/pull/8219) implemented the controller. The actual changes touched 122 files: 45 Go files for the controller logic, 49 YAML files for CRD manifest regeneration, and supporting files. The controller introduces `SecretEncryptionStatus` fields to track the active key, target key, and rotation history. It implements a two-stage KAS rollout pattern — first deploying KAS with the new key added to its encryption config (so it can decrypt both old and new), then triggering the migration, then removing the old key — specifically designed to prevent data loss during the rotation window.

A notable design decision was the deprecation of the `backupKey` fields on `HostedCluster` and `HCP` `SecretEncryptionSpec`. The old design required users to manually manage backup keys during rotation. The new controller automates the entire lifecycle, making the manual backup key concept unnecessary. The deprecation is marked in the API types but the fields remain functional for backward compatibility.

!!! info "Architectural Note"
    The PR also moves `kube-storage-version-migrator` from the data plane to the control plane, enabling re-encryption on zero-worker-node clusters — a topology common in managed services during initial provisioning.

---

## :material-test-tube: CI Release Resolution — Fixing a 45% Failure Rate

There is a particular flavor of CI failure that is both maddening and invisible to anyone not actively watching the dashboards. The HyperShift e2e-aws presubmit job was failing approximately 40–45% of the time before a single test even ran. Not flaky tests. Not infrastructure issues. The tests never started.

The root cause, identified and fixed by [@jparrill](https://github.com/jparrill) in [release PR #79627](https://github.com/openshift/release/pull/79627), was in how ci-operator resolved release images for multi-version NodePool tests. HyperShift's e2e suite creates NodePools at multiple OCP versions (current, N-1, N-2, N-3, N-4) to validate upgrade and cross-version compatibility. The ci-operator configuration had static `release` stanzas that imported these release images at the beginning of every job. The problem: when any of those release images were not yet available in the image stream (because the release controller had not yet produced them, or the import failed), the entire job would fail before a single test binary was compiled.

Six concurrent image imports, any one of which could fail. The math is cruel: even with 90% individual reliability, the probability of all six succeeding is only 53%.

The fix replaced static release imports with runtime resolution. A new `hypershift-resolve-nodepool-releases` step queries the release controller API at test time to find the latest accepted release for each OCP minor version. This means:

- No pre-test image imports that can fail
- The resolution happens after the test binary is built, so compilation errors are caught first
- Concurrent imports drop from 6 to approximately 2–3 (only the versions actually needed)
- If a specific minor version has no accepted release, the test gracefully skips that version instead of failing the entire job

The trade-off is explicit: the hypershift-tests image now requires `jq` and `curl` to be available, adding a runtime dependency. [PR #80023](https://github.com/openshift/release/pull/80023) by [@jparrill](https://github.com/jparrill) added this to an updated test image, and [PR #80576](https://github.com/openshift/release/pull/80576) further hardened the resolver with deep-verification of release component digests.

The change was initially reverted (release #80349) due to an unrelated issue, then re-landed as release #80360 with the fix. The final result: the pre-test failure rate for e2e-aws dropped from ~45% to near zero. For a job that runs on every PR, this reclaimed hundreds of wasted CI hours per week.

---

## :material-robot: Agentic CI Automation — Teaching AI to Tend the CI Garden

The most unusual thread running through these six weeks is not a single PR but a pattern: the team's systematic adoption of AI-assisted development workflows, formalized in the repo's [agentic-sdlc.md](../how-to/agentic-sdlc.md) guide and implemented through a growing set of GitHub Actions workflows and Claude Code skills.

The address-review-comments workflow (PRs #8621, #8639, #8702, #8706, #8720, #8723 by [@bryan-cox](https://github.com/bryan-cox) and [@enxebre](https://github.com/enxebre)) allows an AI agent to read PR review comments, analyze them, and apply the suggested changes. The workflow evolved rapidly over the period: [@bryan-cox](https://github.com/bryan-cox) added the initial workflow, then refined it with `--allowedTools` restrictions, increased timeouts from 30 to 45 minutes, added gopls and pre-commit plugins for code quality, fixed credential file handling on ARC runners, and bumped max turns to 200 for complex reviews. The restructure-commits workflow (PRs #8770, #8771) automates commit reorganization, and a rebase workflow was added alongside it.

In the ai-helpers repository, 36 PRs were merged during this period. [@celebdor](https://github.com/celebdor) added the `/jira:generate-feature-updates` command (ai-helpers #375) for automated weekly status report generation and the `summarize_issue.py` script (ai-helpers #484) for structured issue summaries with author name enrichment. [@enxebre](https://github.com/enxebre) added behavioral evals using promptfoo (ai-helpers #464) and the `validate-pr-override-images` skill (#8616) for verifying CPO override image contents. [@jparrill](https://github.com/jparrill) contributed a `fix-cve` skill (ai-helpers #470) for automated Go CVE patching. The repository is evolving from a collection of scripts into a plugin platform with quality gates (promptfoo evals), self-service contribution workflows, and cross-team adoption.

!!! tip "The Big Picture"
    The [agentic-sdlc.md](../how-to/agentic-sdlc.md) document describes a vision where AI agents handle the mechanical aspects of software development — reformatting code, addressing straightforward review comments, managing dependency updates, triaging CI failures — while human engineers focus on design decisions, architecture, and the kind of judgment calls that the stories in this report illustrate.

---

## :material-star-shooting: Beneath the Headlines

!!! info "297 PRs, 7 stories — what about the rest?"
    The stories above account for fewer than 30 of the 297 merged PRs. Here is a sampling of the other significant work that landed during this period.

**Azure Self-Managed GA Progress.** [@bryan-cox](https://github.com/bryan-cox) drove a relentless campaign toward GA readiness for self-managed Azure HCP. [PR #8088](https://github.com/openshift/hypershift/pull/8088) added KMS encryption support via workload identity federation, introducing `WorkloadIdentity` field to `AzureKMSSpec` with 611 lines of envtest validation. [PR #8480](https://github.com/openshift/hypershift/pull/8480) prevented Azure Private DNS zone shadowing that breaks `*.apps` resolution — an unfixable misconfiguration after creation, defended at four layers (CEL, webhook, CLI, controller). [PR #8537](https://github.com/openshift/hypershift/pull/8537) by [@muraee](https://github.com/muraee) implemented API-driven Azure topology and private connectivity (Phase 1 of the Swift networking abstraction), replacing hardcoded ARO-HCP logic with proper API fields.

**KubeVirt Dual-Stack Networking.** [PR #8421](https://github.com/openshift/hypershift/pull/8421) by [@orenc1](https://github.com/orenc1) added `OVNIPv6Config` with configurable join and transit subnet fields, auto-defaulting KubeVirt clusters to `fd99::/64` to prevent dual-stack routing collisions. This fixed IPv6 LoadBalancer traffic black-holing and required updates to 33 CRD manifests across all feature-gate variants.

**Webhook Cert Bootstrapping.** [PR #8545](https://github.com/openshift/hypershift/pull/8545) by [@clebs](https://github.com/clebs) and [PR #8599](https://github.com/openshift/hypershift/pull/8599) by [@enxebre](https://github.com/enxebre) solved a problem where `hypershift install render` embedded private key material in its output, causing approximately 90,000 certificate reissuances when used with ArgoCD. The fix bootstraps serving certs at operator startup and removes the static `CABundle` from webhook configurations.

**AutoNode/Karpenter Progress.** [@jkyros](https://github.com/jkyros) fixed a status clobbering bug ([#8562](https://github.com/openshift/hypershift/pull/8562)) where multiple controllers were fighting over HCP status, causing the AutoNode condition to be overwritten. [@maxcao13](https://github.com/maxcao13) improved Karpenter node cleanup on CAPI Cluster deletion ([#8589](https://github.com/openshift/hypershift/pull/8589)) and fixed test infrastructure for multi-arch ARM64 tests.

**VPC Endpoint AZ Fix.** [PR #8651](https://github.com/openshift/hypershift/pull/8651) by [@reedcort](https://github.com/reedcort) deduplicated VPC endpoint subnets by availability zone, fixing indefinite reconciliation failures for multi-NodePool clusters sharing AZs.

**Production Bug Fixes.** [@rafael-azevedo](https://github.com/rafael-azevedo) fixed a cascading HA failure where an invalid pull secret caused the entire reconciliation loop to exit early ([#8352](https://github.com/openshift/hypershift/pull/8352)). [@dhgautam99](https://github.com/dhgautam99) fixed kubeadmin password hash reconciliation after etcd restoration ([#8358](https://github.com/openshift/hypershift/pull/8358)). [@vsolanki12](https://github.com/vsolanki12) fixed NodePool reconciliation failures with immutable ConfigMaps ([#8543](https://github.com/openshift/hypershift/pull/8543)). [@csrwng](https://github.com/csrwng) propagated management cluster proxy env vars to konnectivity sidecar ([#8569](https://github.com/openshift/hypershift/pull/8569)). [@PoornimaSingour](https://github.com/PoornimaSingour) fixed in-place upgrade stalls from terminated MCD pods ([#8434](https://github.com/openshift/hypershift/pull/8434)).

---

## :material-chart-bar: By the Numbers

| Metric | Value |
|--------|-------|
| Total PRs merged | 297 |
| Unique contributors | 43 |
| Bot PRs | 26 |
| HyperShift repo PRs | 180 |
| Release repo PRs | 71 |
| AI-helpers repo PRs | 36 |
| Enhancement proposals | 10 |
| Average merge time | 213.6 hours |
| High-impact PRs | 31 |
| Breaking changes | 18 |
| API changes | 11 |
| Customer-reported fixes | 14 |

**Top Reviewers**

| Reviewer | PRs Reviewed |
|----------|-------------|
| [@coderabbitai](https://github.com/apps/coderabbitai) | 164 |
| [@bryan-cox](https://github.com/bryan-cox) | 78 |
| [@jparrill](https://github.com/jparrill) | 41 |

---

## :octicons-people-24: Contributors

Click any column header to sort. Each number links to the contributor's PRs in that repository.

| Contributor | hypershift | release | ai-helpers | enhancements | Total |
|------------|:-:|:-:|:-:|:-:|:-:|
| [@bryan-cox](https://github.com/bryan-cox) | [45](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-05-14..2026-06-22) | [11](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Abryan-cox+merged%3A2026-05-14..2026-06-22+hypershift) | [1](https://github.com/openshift-eng/ai-helpers/pull/528) | [1](https://github.com/openshift/enhancements/pull/2016) | **58** |
| [@enxebre](https://github.com/enxebre) | [17](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-05-14..2026-06-22) | [4](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-05-14..2026-06-22+hypershift) | [2](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Aenxebre+merged%3A2026-05-14..2026-06-22) | [1](https://github.com/openshift/enhancements/pull/2019) | **24** |
| [@celebdor](https://github.com/celebdor) | [16](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-05-14..2026-06-22) |  | [2](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Acelebdor+merged%3A2026-05-14..2026-06-22) |  | **18** |
| [@mgencur](https://github.com/mgencur) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-05-14..2026-06-22) | [15](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amgencur+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **18** |
| [@jparrill](https://github.com/jparrill) | [10](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-05-14..2026-06-22) | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-05-14..2026-06-22+hypershift) | [2](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Ajparrill+merged%3A2026-05-14..2026-06-22) |  | **15** |
| [@cblecker](https://github.com/cblecker) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acblecker+merged%3A2026-05-14..2026-06-22) | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Acblecker+merged%3A2026-05-14..2026-06-22+hypershift) | [3](https://github.com/openshift-eng/ai-helpers/pulls?q=is%3Apr+is%3Amerged+author%3Acblecker+merged%3A2026-05-14..2026-06-22) |  | **10** |
| [@maxcao13](https://github.com/maxcao13) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amaxcao13+merged%3A2026-05-14..2026-06-22) | [1](https://github.com/openshift/release/pull/79262) |  |  | **6** |
| [@dhgautam99](https://github.com/dhgautam99) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adhgautam99+merged%3A2026-05-14..2026-06-22) |  |  |  | **5** |
| [@muraee](https://github.com/muraee) | [4](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Amuraee+merged%3A2026-05-14..2026-06-22) |  |  | [1](https://github.com/openshift/enhancements/pull/1969) | **5** |
| [@sdminonne](https://github.com/sdminonne) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Asdminonne+merged%3A2026-05-14..2026-06-22) |  |  |  | **5** |
| [@vsolanki12](https://github.com/vsolanki12) | [5](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avsolanki12+merged%3A2026-05-14..2026-06-22) |  |  |  | **5** |
| [@orenc1](https://github.com/orenc1) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Aorenc1+merged%3A2026-05-14..2026-06-22) | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Aorenc1+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **4** |
| [@stephenfin](https://github.com/stephenfin) |  | [4](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Astephenfin+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **4** |
| [@cristianoveiga](https://github.com/cristianoveiga) | [1](https://github.com/openshift/hypershift/pull/8446) | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Acristianoveiga+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **3** |
| [@csrwng](https://github.com/csrwng) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Acsrwng+merged%3A2026-05-14..2026-06-22) | [1](https://github.com/openshift/release/pull/78912) |  |  | **3** |
| [@PoornimaSingour](https://github.com/PoornimaSingour) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3APoornimaSingour+merged%3A2026-05-14..2026-06-22) |  |  |  | **3** |
| [@shwetam2004](https://github.com/shwetam2004) |  | [3](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Ashwetam2004+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **3** |
| [@vismishr](https://github.com/vismishr) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Avismishr+merged%3A2026-05-14..2026-06-22) | [1](https://github.com/openshift/release/pull/80186) |  |  | **3** |
| [@yiraeChristineKim](https://github.com/yiraeChristineKim) | [3](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3AyiraeChristineKim+merged%3A2026-05-14..2026-06-22) |  |  |  | **3** |
| [@clebs](https://github.com/clebs) | [1](https://github.com/openshift/hypershift/pull/8545) | [1](https://github.com/openshift/release/pull/73748) |  |  | **2** |
| [@dustman9000](https://github.com/dustman9000) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Adustman9000+merged%3A2026-05-14..2026-06-22) |  |  |  | **2** |
| [@galchammat](https://github.com/galchammat) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Agalchammat+merged%3A2026-05-14..2026-06-22) |  |  |  | **2** |
| [@jkyros](https://github.com/jkyros) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Ajkyros+merged%3A2026-05-14..2026-06-22) |  |  |  | **2** |
| [@mtulio](https://github.com/mtulio) |  | [2](https://github.com/openshift/release/pulls?q=is%3Apr+is%3Amerged+author%3Amtulio+merged%3A2026-05-14..2026-06-22+hypershift) |  |  | **2** |
| [@Nirshal](https://github.com/Nirshal) | [1](https://github.com/openshift/hypershift/pull/8585) | [1](https://github.com/openshift/release/pull/80420) |  |  | **2** |
| [@typeid](https://github.com/typeid) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3Atypeid+merged%3A2026-05-14..2026-06-22) |  |  |  | **2** |
| [@YamunadeviShanmugam](https://github.com/YamunadeviShanmugam) | [2](https://github.com/openshift/hypershift/pulls?q=is%3Apr+is%3Amerged+author%3AYamunadeviShanmugam+merged%3A2026-05-14..2026-06-22) |  |  |  | **2** |
| [@Ajpantuso](https://github.com/Ajpantuso) | [1](https://github.com/openshift/hypershift/pull/8689) |  |  |  | **1** |
| [@amasolov](https://github.com/amasolov) | [1](https://github.com/openshift/hypershift/pull/8311) |  |  |  | **1** |
| [@ashishmax31](https://github.com/ashishmax31) | [1](https://github.com/openshift/hypershift/pull/8312) |  |  |  | **1** |
| [@Atharva-Shinde](https://github.com/Atharva-Shinde) | [1](https://github.com/openshift/hypershift/pull/8443) |  |  |  | **1** |
| [@ckandag](https://github.com/ckandag) | [1](https://github.com/openshift/hypershift/pull/8413) |  |  |  | **1** |
| [@cssjr](https://github.com/cssjr) | [1](https://github.com/openshift/hypershift/pull/8703) |  |  |  | **1** |
| [@ehearne-redhat](https://github.com/ehearne-redhat) | [1](https://github.com/openshift/hypershift/pull/8716) |  |  |  | **1** |
| [@jiezhao16](https://github.com/jiezhao16) |  | [1](https://github.com/openshift/release/pull/79884) |  |  | **1** |
| [@Joeavaikath](https://github.com/Joeavaikath) |  | [1](https://github.com/openshift/release/pull/80356) |  |  | **1** |
| [@joshbranham](https://github.com/joshbranham) | [1](https://github.com/openshift/hypershift/pull/8504) |  |  |  | **1** |
| [@raelga](https://github.com/raelga) | [1](https://github.com/openshift/hypershift/pull/8509) |  |  |  | **1** |
| [@rafael-azevedo](https://github.com/rafael-azevedo) | [1](https://github.com/openshift/hypershift/pull/8352) |  |  |  | **1** |
| [@reedcort](https://github.com/reedcort) | [1](https://github.com/openshift/hypershift/pull/8651) |  |  |  | **1** |
| [@rutvik23](https://github.com/rutvik23) | [1](https://github.com/openshift/hypershift/pull/8331) |  |  |  | **1** |
| [@smrtrfszm](https://github.com/smrtrfszm) | [1](https://github.com/openshift/hypershift/pull/8484) |  |  |  | **1** |
| [@twolff-gh](https://github.com/twolff-gh) | [1](https://github.com/openshift/hypershift/pull/8472) |  |  |  | **1** |

---

## :material-crystal-ball: What's Next

The work merged in this period sets up several major threads for the coming months.

**CAPI v1beta2 full migration.** PR #8594 installed v1beta2 as a served version with v1beta1 still as storage. The next phase is enabling conversion webhooks, validating the migration path, and eventually flipping the storage version — a careful, multi-release process.

**Azure Swift Phases 2–3.** PR #8537 landed Phase 1 of the Azure topology abstraction. Phases 2 and 3 will extend the API-driven model to cover shared ingress and additional private connectivity scenarios, further decoupling self-managed Azure from hardcoded ARO-HCP assumptions.

**OCP 5.0 multi-stream rollout.** The dual-stream RHEL infrastructure is in place behind a TechPreview gate. The next step is extending stream-aware boot image resolution beyond AWS to GCP, Azure, KubeVirt, and other platforms, followed by e2e validation and eventual promotion to GA.

**Kubernetes 1.36 upgrade.** PR #8683 by [@bryan-cox](https://github.com/bryan-cox) already removed the controller-runtime pin for the k8s 1.36 bump. The CAPI 1.11 migration cleared the dependency path. The full k8s 1.36 upgrade is the next major dependency milestone.

**Konflux release gating pipeline.** Enhancement #2016 by [@bryan-cox](https://github.com/bryan-cox) designed a nightly pipeline that validates HyperShift Operator images against platform-specific e2e tests before promotion. Implementation is underway, with the gangway token for hypershift-pool-admins already landed.

The pace is not slowing down.
