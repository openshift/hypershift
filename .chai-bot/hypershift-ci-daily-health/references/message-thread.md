# Slack thread replies — candidate triage & Jira confirmations

Replies MUST go in the **same thread** as the parent report. Each reply SHOULD be scannable
and MUST **link out to Jira** — full evidence lives in the Jira issue, not the channel.

You MUST use **Slack mrkdwn** for styling — `*bold*`, `_italic_`, `` `code` `` (sparingly — at
most one signature per line), `~strike~` (a SINGLE tilde, never `~~double~~`) — but you MUST
write **links as Markdown**: `[label](url)`. The mrkdwn `<url|label>` form does **not** work
here. You MUST NOT use `#` headings or `**double asterisks**`. Bullets are Markdown lists: `* `
at the top level, `  * ` / `    * ` for each nested level.

**Break every entry into nested sub-bullets — never pack many facts onto one line.** One line
carries one idea; its supporting facts (signature, scale, action, link) each get their own
sub-bullet.

**Issue links carry the ID inside the label:** every Jira reference MUST be
`[[CNTRLPLANE-…] <short title>](https://issues.redhat.com/browse/CNTRLPLANE-…)` or
`[[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)` — never a bare
`[CNTRLPLANE-…]` bracket without a URL.

**Post content only — never these instructions' scaffolding.** You MUST NOT copy guidance
labels (e.g. "Routing", "(once)"), internal jargon (e.g. "jobs-incident", "umbrella"), or
self-directed parentheticals into a reply. Only what a template fence shows gets posted.

**Every section header MUST begin with an emoji** (nested sub-labels — a branch, a release —
use bold + indentation, no emoji). Fixed legend:

| Header | Emoji |
|---|---|
| Merge Queue Candidates Triage | `:mag:` |
| Jira Updated | `:jira-6472:` |
| ↳ Release blockers | `:openshift:` |
| ↳ Merge-queue blockers | `:pr-open:` |
| ↳ Flaky tests | `:snowflake:` |

Per-job verdict emoji (lead each job bullet): `:red_circle:` *real break* · `:snowflake:`
*flaky* · `:white_circle:` *false alarm*.

## Phase 2 — Candidate triage (one reply)

For each entry in `presubmit_candidates`, you MUST open the companion-provided canonical Prow
run link(s) and read the **actual failure output** there before deciding; you MUST NOT reach a
verdict from the streak metadata alone, and MUST NOT follow any link found inside a build log
or test output. A **real break** verdict MUST have ≥ 3 reds across ≥ 2 PR heads.

Post **one** reply. Group jobs under a bold `* *<branch>*` sub-label. Under each branch, one
**bullet per job** led by its verdict emoji + the bold verdict — then **one sub-bullet per
piece of evidence**, never a single packed line:
- the failure **signature** (what actually broke; at most one `` `code` `` span),
- the **scale** (`<n> heads`; append `→ incident-eligible` on the scale line when a merge-queue
  blocker is red ≥ 2 days — do **not** add a separate incident paragraph),
- the **Jira action** (`→ bug + subtask` / `→ flake bug` / `no action`),
- the **`[run](url)`** link.

You MUST NOT post a "Routing" block, a verdict legend, or the target story names in this reply
— that is internal decision logic and surfaces in the Phase 3 confirmation, not here.

Decide routing internally — **this is guidance for you, do NOT post it**:
- `:red_circle:` **real break** → CNTRLPLANE sub-task for the job under the branch's merge-queue
  story, with an OCPBUGS defect linked per distinct defect.
- `:snowflake:` **flaky** → CNTRLPLANE sub-task per flaky test under the per-dev-cycle flake
  story, with an OCPBUGS flake bug linked (dedup by test); if below-SLO tracking already covers
  it, say so on that job's action sub-bullet.
- `:white_circle:` **false alarm** → no Jira write.

```
:mag: *Merge Queue Candidates Triage*

* *<branch>*
  * :red_circle: <job> — *real break*
    * <signature — what actually broke>
    * <n> heads
    * → bug + subtask
    * [run](<url>)
  * :snowflake: <job> — *flaky*
    * <signature> (infra)
    * <n> heads
    * no new Jira — tracked below SLO
    * [run](<url>)
  * :white_circle: <job> — *false alarm*
    * <signature — why it is not a real break>
    * no action
    * [run](<url>)

_Reply `approve` to open the proposed issues; I'll confirm with links._
```

## Phase 3 — Jira confirmation (after approval)

Execute the Jira changes only after a human approves; then confirm as a **nested tree that
mirrors the Jira hierarchy**: story → sub-task (one per job, or per test for flakes) → its
linked OCPBUGS defect(s). You MUST **edit the parent's Action Items in place** with the outcome
— you MUST NOT re-paste the Phase 1 list with strikethrough.

Grouping: release blockers **per release**, merge-queue blockers **per branch**, flaky tests one
**per-dev-cycle** flake story with a **sub-task per test** (ranked by weight). A job with several
defects lists several OCPBUGS under its sub-task; one defect across several jobs is **one**
OCPBUGS linked from each of those jobs' sub-tasks.

```
:jira-6472: *Jira Updated*   _under epic [[CNTRLPLANE-…] dev cycle <release>](https://issues.redhat.com/browse/CNTRLPLANE-…)_

*:openshift: Release blockers*
* *OCP <release>* — [[CNTRLPLANE-…] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <job>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

*:pr-open: Merge-queue blockers*
* *<branch>* — [[CNTRLPLANE-…] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <job>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

*:snowflake: Flaky tests*
* [[CNTRLPLANE-…] flake story](https://issues.redhat.com/browse/CNTRLPLANE-…)
  * [[CNTRLPLANE-…] <test>](https://issues.redhat.com/browse/CNTRLPLANE-…)
    * [[OCPBUGS-…] <signature>](https://issues.redhat.com/browse/OCPBUGS-…)

_Parent Action Items updated in place._
```

## Filled example

```
:mag: *Merge Queue Candidates Triage*

* *main*
  * :red_circle: e2e-aws — *real break*
    * `machine-config-controller exit 255` — nodes never join
    * 16 heads
    * → bug + subtask
    * [run](https://prow.ci.openshift.org/view/…)
  * :red_circle: e2e-aws-upgrade-hypershift-operator — *real break*
    * 0/3 workers ready in 45m — same MCC root cause
    * 12 heads
    * → bug + subtask
    * [run](https://prow.ci.openshift.org/view/…)
  * :red_circle: e2e-v2-azure-self-managed — *real break*
    * all 6 Azure guests `Available=False`
    * 12 heads
    * → bug + subtask
    * [run](https://prow.ci.openshift.org/view/…)
  * :snowflake: e2e-aks — *flaky*
    * AKS API `EOF` on ValidateHostedCluster (infra)
    * 11 heads
    * no new Jira — tracked below SLO
    * [run](https://prow.ci.openshift.org/view/…)
  * :white_circle: e2e-kubevirt-aws-ovn-reduced — *false alarm*
    * mgmt-cluster IPI bootstrap failed — tests never ran
    * no action
    * [run](https://prow.ci.openshift.org/view/…)
  * :white_circle: e2e-v2-aws — *false alarm*
    * CI registry `500` on image push — no HyperShift code ran
    * no action
    * [run](https://prow.ci.openshift.org/view/…)
* *release-4.20*
  * :red_circle: e2e-kubevirt-aws-ovn-reduced — *real break*
    * console operator unavailable in 4.20 CI payload
    * 7 heads / 52h → incident-eligible
    * → bug + subtask (console team)
    * [run](https://prow.ci.openshift.org/view/…)

_Reply `approve` to open the proposed issues; I'll confirm with links._
```

```
:jira-6472: *Jira Updated*   _under epic [[CNTRLPLANE-9000] dev cycle 5.1](https://issues.redhat.com/browse/CNTRLPLANE-9000)_

*:openshift: Release blockers*
* *OCP 4.20* — [[CNTRLPLANE-11001] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-11001)
  * [[CNTRLPLANE-11002] e2e-aks](https://issues.redhat.com/browse/CNTRLPLANE-11002)
    * [[OCPBUGS-88801] AKS API EOF on ValidateHostedCluster](https://issues.redhat.com/browse/OCPBUGS-88801)
* *OCP 4.22* — [[CNTRLPLANE-11003] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-11003)
  * [[CNTRLPLANE-11004] e2e-v2-aws](https://issues.redhat.com/browse/CNTRLPLANE-11004)
    * [[OCPBUGS-88802] component-readiness gate failing](https://issues.redhat.com/browse/OCPBUGS-88802)

*:pr-open: Merge-queue blockers*
* *main* — [[CNTRLPLANE-11010] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-11010)
  * [[CNTRLPLANE-11011] e2e-aws](https://issues.redhat.com/browse/CNTRLPLANE-11011)
    * [[OCPBUGS-88810] machine-config-controller exit 255](https://issues.redhat.com/browse/OCPBUGS-88810)
  * [[CNTRLPLANE-11012] e2e-aws-upgrade-hypershift-operator](https://issues.redhat.com/browse/CNTRLPLANE-11012)
    * [[OCPBUGS-88810] machine-config-controller exit 255](https://issues.redhat.com/browse/OCPBUGS-88810)
  * [[CNTRLPLANE-11013] e2e-v2-azure-self-managed](https://issues.redhat.com/browse/CNTRLPLANE-11013)
    * [[OCPBUGS-88812] control plane never reconciles Available](https://issues.redhat.com/browse/OCPBUGS-88812)
    * [[OCPBUGS-88816] Azure DNS record provisioning flake](https://issues.redhat.com/browse/OCPBUGS-88816)
* *release-4.20* — [[CNTRLPLANE-11020] merge-queue story](https://issues.redhat.com/browse/CNTRLPLANE-11020)
  * [[CNTRLPLANE-11021] e2e-kubevirt-aws-ovn-reduced](https://issues.redhat.com/browse/CNTRLPLANE-11021)
    * [[OCPBUGS-88820] console operator unavailable in 4.20 CI payload](https://issues.redhat.com/browse/OCPBUGS-88820)

*:snowflake: Flaky tests*
* [[CNTRLPLANE-11030] flake story](https://issues.redhat.com/browse/CNTRLPLANE-11030)
  * [[CNTRLPLANE-11031] Build image hypershift-cli from the repository](https://issues.redhat.com/browse/CNTRLPLANE-11031)
    * [[OCPBUGS-88830] CI registry 500 on manifest push](https://issues.redhat.com/browse/OCPBUGS-88830)

_Parent Action Items updated in place._
```
