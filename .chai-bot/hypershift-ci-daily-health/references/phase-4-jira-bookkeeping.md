# Phase 4 — Jira reconciliation (all Jira work)

## Overview

**All** Jira work for the run happens here — no other phase writes Jira. You consolidate what the earlier
phases found — the release blockers (Phase 1), the triaged merge-queue breaks and flaky tests (Phase 2),
and the periodics-health lifecycle (Phase 3) — and **reconcile** Jira to today's reality: search first,
then create what is missing, update what drifted, and close what is resolved. This is the pipeline's
Phase 4 (see *Pipeline & run directory* in the main prompt). The hierarchy, each tier's identity
convention, the labels, the OCPSTRAT create-template, and the link/transition mechanics are in
`.chai-bot/hypershift-ci-daily-health/references/jira-model.md` — follow it exactly.

## Inputs

**Constraints:**
- On start you MUST read, from the run directory `/tmp/hypershift-ci-daily-health/<run day>/`:
  `journal.md`, `data.json`, `phase-1-handoff.md` (release blockers), `phase-2-handoff.md` (triage
  verdicts), and `phase-3-handoff.md` (periodics-health buckets + fidelity findings); and `jira-model.md`.
- The SLI `rate` comes from `data.json` (enriched `release_blockers[].rate` for a blocker; the
  `phase-3-handoff.md` rate for a periodic) and the observation time is `data.json` `generated_at`. You
  MUST NOT invent a rate **because** fabricating an SLI writes a false number into a living record; if the
  rate is null/absent there, record an Outstanding gap and write **no** SLI (never a literal `None%`).
- The **flake** story's sub-tasks are sourced from `data.json` `flaky_tests[]` (one per flaky test,
  identity is the verbatim `test_name`), folding in any `phase-2-handoff.md` flaky verdict for the same test.
- If any of `phase-1/2/3-handoff.md` is missing or empty, you MUST fail closed for that domain: make NO
  writes for it and record it under Outstanding gaps **because** a missing handoff is never evidence that
  a tracked blocker resolved.

## Steps

### 1. Reconcile the spine, searching before every create
Walk `OCPSTRAT Feature → epic → story → sub-task → linked OCPBUGS` top-down, finding each tier by its
identity convention before you create anything.

**Constraints:**
- For each tier you MUST find the existing issue by its **identity convention** in `jira-model.md`, then
  reconcile on how many you find: when none matches, create it; when exactly one matches, reuse it and
  create nothing; when more than one matches, write nothing and record the duplicate (every key) under
  Outstanding gaps. You MUST NOT "pick the most likely" among several **because** a wrong guess mutates
  production Jira.
- **Creating an OCPSTRAT Feature is a last resort — it is human-owned.** You MUST create one only when
  none exists, and MUST NOT create one on an ambiguous match of more than one **because** a
  duplicate strategic Feature is a costly human cleanup.
- You MUST create a node only after its parent exists and its key is in hand (create strictly top-down)
  **because** a child create fails without its parent key.
- You MUST create CNTRLPLANE issues in `[jira].story_project` and defects in `[jira].defect_project`, and
  you MUST label every CNTRLPLANE issue with `[jira].label` on `[jira].component`.

### 2. Pin sub-task identity to the job
Find a job's sub-task by the job, never by its summary.

**Constraints:**
- You MUST match an existing sub-task by its **`job_id`** (which MUST appear verbatim in every sub-task
  summary you create) under the correct story, and you MUST NOT dedup on the summary text **because** an
  SLO-tracker summary carries the per-run SLI and changes every run, so matching on it creates a duplicate
  sub-task on day two and orphans the old one's history.
- You MUST match a **flake** sub-task by its **`test_name`** (verbatim in the summary, never the volatile
  jobs/branches list) **because** the same day-two-duplicate risk applies to flaky tests.

### 3. Record the SLI idempotently
Every SLO-tracker sub-task (every periodics-health sub-task, and a release-blocker's per-job sub-task)
records the observed SLI.

**Constraints:**
- The SLI is `<rate>% @ <observation time>` (rate from `data.json`; observation time is `generated_at`,
  RFC3339). On create, you MUST put it in the summary and start the description history. On update, you
  MUST **replace** the summary's SLI with today's reading and **append** the observation to the
  description's running history — appending **only if that `@ <observation time>` is not already present**
  **because** a re-run against the same `data.json` must not double-count — and you MUST NOT overwrite
  prior observations **because** the history is the cross-day SLI audit trail.
- Merge-queue and flake sub-tasks are not SLO trackers, so you MUST NOT give them a rate@as-of.

### 4. Reconcile the OCPBUGS defects
Link one OCPBUGS per distinct defect; search before creating.

**Constraints:**
- Before creating any OCPBUGS you MUST search for an existing one (the convention is in `jira-model.md`)
  and scan the OCPBUGS already linked from sibling sub-tasks, then reconcile on how many match: when none
  matches, create one; when exactly one matches the same defect, link it; when more than one matches
  ambiguously, link none and record an Outstanding gap. Two failures are the **same defect** only when the
  failing test/step and the error signature match.
- You MUST NOT re-point an existing OCPBUGS at a different defect **because** one OCPBUGS tracks one
  defect for life; a different defect always gets a new OCPBUGS. A defect spreading to more jobs is the
  **same** OCPBUGS, linked from each affected job's sub-task.

### 5. Fold in the lifecycle and close what resolved
Apply the Phase-3 periodics-health lifecycle, and close sub-tasks whose jobs recovered.

**Constraints:**
- You MUST apply each lifecycle verb as its specific Jira op (full mechanics in `jira-model.md`):
  **open** — create the sub-task (unassigned); **nudge** — add a dated comment with no field change;
  **escalate** — add the `[jira].fix_or_retire_label` and a dated comment with no status or assignee
  change; **close** — the named close transition with its resolution.
- **Close-diff (all story kinds):** for each open sub-task under a release-blocker / merge-queue /
  periodics-health story whose `job_id` is **absent** from today's set (today's `incident_set` /
  Phase-2 real-break set / below-SLO set) **and** whose defect is confirmed gone, you MUST close it. You MUST NOT
  close a sub-task whose job still appears in today's set **because** that would drop live tracking. You
  MUST skip the close-diff for any domain whose handoff was missing/empty (per Inputs) **because** an
  empty set would otherwise read as "everything recovered" and mass-close live tracking.
- **Close the merge-queue story when its branch clears:** once none of a per-branch merge-queue story's
  presubmits is still a real break this run, you MUST transition the **story** (not only its sub-tasks) to Closed
  **because** the unresolved story's creation time is the branch-blocked-since clock the Phase-5 handoff
  incident reads — leaving it open recommends a phantom incident forever.
- You MUST update **only** the SLO-tracker summary/description SLI and the OCPBUGS link set, and you MUST
  NOT modify assignee, priority, or status (except the defined close) **because** those are human-owned
  and a blind update clobbers their edits. A flake sub-task's "weight" is not yet implemented — you MUST
  NOT set or fabricate one.

### 6. Adversarial self-review (gate before posting)
Before posting, spin up an adversarial reviewer sub-agent and resolve every item it flags.

**Constraints:**
- You MUST run this review and fix anything flagged — including fixing the Jira — **before** posting.
- Give the reviewer this checklist:
  - **Formatting matches the committed template** — headers, divider bars, bullet hierarchy, bold/code/
    link conventions; no `#`, no `**`, no leftover `$placeholder`; no `@@` keys or `<--` notes leaked.
  - **Every item in the P1/P2/P3 handoffs has a corresponding action** in the reconciled tree (or an
    explicit Outstanding-gap note).
  - **No duplicate tickets** created; **every Jira reference is a Markdown link** to its
    `issues.redhat.com` URL with the ID inside the label — never a bare key.
  - The SLI reads `<rate>% @ <as-of>` and was **appended** (history intact), not overwritten.
  - The posted tree matches what was actually written to Jira.

## Output: Slack message

Author only the section bodies into `$RUN_DIR/phase-4-bodies.txt` (sections separated by `@@ <key>`),
then render and post:

```text
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py render-message \
  --phase 4 --bodies "$RUN_DIR/phase-4-bodies.txt" --out "$RUN_DIR/phase-4-message.txt"
```

Post `$RUN_DIR/phase-4-message.txt` **verbatim**. The template owns the `:jira-6472: Jira reconciliation`
title, the `Existing tracking hierarchy` / `:openshift_flat: Release blockers` / `:pr-open: Merge-queue
blockers` / `:snowflake: Flaky tests` / `:hourglass: Periodics health` / `:mag: Defect reconciliation` /
`:warning: Outstanding gaps` headers, the bars, and the `(part 4/5)` footer.

**Sections** (one `@@ <key>` each, all required; an empty group gets a `• None …` body):
`existing_hierarchy`, `release_blockers`, `merge_queue`, `flaky`, `periodics_health`,
`defect_reconciliation`, `outstanding`.

**Body styling** (main-prompt rules): releases/branches are bold sub-labels (`*OCP <release>*`,
`*<branch>*`); **every Jira issue reference MUST be a Markdown link** to its `issues.redhat.com` URL with
the ID inside the label — `[[CNTRLPLANE-…] <short title>](url)`, never a bare `[ID]`; nest `• ` story, then
`  ◦ ` sub-task, then `    :black_small_square: ` linked defect. An SLO-tracker sub-task shows its SLI in the
summary; use `:exclamation: opened` / `:done-circle-check: closed` for lifecycle outcomes.

### Example `phase-4-bodies.txt`
This is the real file. The `<--` notes explain the Slack formatting mechanic — **do not put them in the
actual file.**

```
@@ existing_hierarchy
• [[OCPSTRAT-3645] Release and Pipeline Maintenance for Hosted Control Planes (5.1)](https://issues.redhat.com/browse/OCPSTRAT-3645) — per-release feature.   <-- every Jira issue is linked (ID inside the [ ] label)
• [[CNTRLPLANE-4605] HyperShift CI Daily Health — 5.1](https://issues.redhat.com/browse/CNTRLPLANE-4605) — dev-cycle epic.
@@ release_blockers
• *OCP 4.22* — [[CNTRLPLANE-11003] release-blocker story](https://issues.redhat.com/browse/CNTRLPLANE-11003)   <-- bold release via * *
  ◦ [[CNTRLPLANE-11004] e2e-v2-aws (0% @ 2026-10-07T19:38:36Z)](https://issues.redhat.com/browse/CNTRLPLANE-11004)   <-- SLO tracker: rate @ observation-time in the summary
    :black_small_square: [[OCPBUGS-88802] component-readiness gate failing](https://issues.redhat.com/browse/OCPBUGS-88802)   <-- level 3: four spaces, then :black_small_square:
@@ merge_queue
• None: no confirmed merge-queue break this run.
@@ flaky
• None: no flaky tests to track this run.
@@ periodics_health
• *OCP 5.1*
  ◦ :exclamation: opened [[CNTRLPLANE-12101] restore e2e-aks (40% @ 2026-10-07T19:38:36Z)](https://issues.redhat.com/browse/CNTRLPLANE-12101)
  ◦ :done-circle-check: closed [[CNTRLPLANE-11990] restore e2e-aws-conformance](https://issues.redhat.com/browse/CNTRLPLANE-11990) — recovered above SLO.
@@ defect_reconciliation
• `e2e-aks` (5.1): EnsureNoCrashingPods confirmed; no exact historical-defect match — new OCPBUGS deferred until the signature is isolated.
@@ outstanding
• Two release-blocker signatures are not yet isolated; defects not linked until confirmed.
```

## Output: handoff + journal

**Constraints:**
- Append a dated entry to `journal.md`, and write `phase-4-handoff.md` as the reconciled tree with fixed
  sections `## created`, `## updated`, `## closed`, `## outstanding`, each a list of
  `<ISSUE-KEY> | <url> | <one-line>`. Phase 5 reads these keys/links for the Team Action Items, so the
  shape MUST be the same every run.

## Definition of done
Every handoff item searched and given exactly one create/update/close outcome (or an Outstanding-gap
note); `phase-4-message.txt` posted verbatim; `phase-4-handoff.md` written in the schema above; the
`journal.md` entry appended; the adversarial review passed with no open items.
