# Phase 5 — Incident, action items & report

## Overview

Phase 5 is the final, consolidated message — the confirmed picture after triage (Phase 2) and periodics
health (Phase 3), posted **independently of Phase 4** (it runs whether or not Phase 4 wrote anything).
Three things go out together: the **one recommended incident**, the **collective Team Action Items**, and
the **attached HTML report**. This is the pipeline's Phase 5 (see *Pipeline & run directory* in the main
prompt).

## Inputs

**Constraints:**
- Resolve the run directory from the run's as-of (Step 1), then read: `journal.md`, `data.json`
  (`incident_set`, `at_a_glance`), `phase-2-handoff.md`, `phase-3-handoff.md`, and (if present)
  `phase-4-handoff.md` (for the action-item tracker links).
- `phase-4-handoff.md` is OPTIONAL — if it is absent/partial, proceed anyway **because** Phase 5 is
  independent of Phase 4; cite only tracker links that actually appear, and for anything untracked phrase
  the action item as "open and assign the tracker" with NO invented issue key.
- You MUST read Jira fresh for cross-day context (a merge-queue branch's blocked-since is its unresolved
  story's creation time) **because** the data document cannot carry yesterday.

## Steps

### 1. Resolve the run directory
Point at the directory for this run's as-of, not wall-clock today.

**Constraints:**
- `RUN_DIR` is the directory for the `--as-of` UTC day recorded in `journal.md` (or the
  most-recently-modified directory under `/tmp/hypershift-ci-daily-health/` if the date is unclear). You
  MUST NOT derive it from `date -u +%F` **because** a backfilled or midnight-spanning run would point at
  the wrong or an empty directory. If the chosen directory lacks `data.json`, STOP and report `Unknown`.

### 2. Determine the incident
Recommend exactly one incident covering all blocked releases/branches — only for blockers meeting the
strict criteria in `jira-model.md`.

**Constraints:**
- The incident has **three** sources; you MUST union all three:
  1. every `data.json` `incident_set` entry (permafailing release / merge-queue blockers — at least 2 days red
     by construction);
  2. every `phase-2-handoff.md` entry whose **`incident_eligible`** flag is true (Phase 2's exact field);
  3. any merge-queue branch whose unresolved story's creation time is **more than
     `[incident].blocked_days_threshold` days** ago (a branch blocked across sub-2-day handoffs with no
     single permafailing job), computed as now minus the story's creation time.
- You MUST NOT search the handoff for "confirmed as permafailing" **because** Phase 2 never writes that
  phrase — relying on it would drop every merge-queue break from the incident.
- You MUST recommend an incident **only** for the above; you MUST NOT escalate a below-SLO rate, a
  degrading trend, a flaky test, or a sub-2-day blocker **because** those are not incident-eligible.
- A permafailing blocker is incident-eligible on its own — you MUST look up **or open** its tracking
  story and MUST NOT withhold the recommendation because no story exists yet.
- You recommend; the team declares. You MUST NOT declare, open, or transition an incident yourself.

### 3. Write the report annotations
Author the narrative the deterministic tables cannot.

**Constraints:**
- The report's deterministic tables are the **Phase-1 collection snapshot**; the annotations are the
  **only** lever that carries the end-of-run deltas. So in `summary`/`incident` you MUST reconcile any
  Phase-1 table figure that triage changed (e.g. candidates Phase 2 resolved, a break Phase 2 confirmed)
  **because** otherwise the attached report contradicts the Slack recommendation.
- You MUST author a `summary` (and an `incident` whenever an incident exists) — a numbers-only report is
  a failure of this phase.
- `job_notes` keys MUST be the **exact `job_id`** string from `data.json` (not the display `name`), and
  you MUST verify each key matches a real `job_id` before rendering **because** the renderer looks notes
  up by `job_id` and silently drops a note under any other key.
- You MUST write the annotations to `$RUN_DIR/annotations.json`; the field rules are in *Writing the
  report narrative* below.

### 4. Render the report
```text
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py report \
  --data "$RUN_DIR/data.json" --annotations "$RUN_DIR/annotations.json" --html-out "$RUN_DIR/report.html"
```

### 5. Author the Slack bodies, render, post, and attach
Post the final message and attach the report.

**Constraints:**
- You MUST author the **one** incident once (Step 2) and derive **both** the `incident` annotation and
  the `@@ incident` Slack body from it — they MUST name the same releases/branches, the same count, and
  the same durations **because** a manager cross-reading the two must not see two stories.
- You MUST source Team Action Items only from the handoff facts — enumerate each that is present, and invent none:
  - declare and handle the one incident, if any;
  - for each unassigned high-priority sub-task open 1–2 days with no progress, assign an owner;
  - for each blocker under 2 days old missing a major/critical bug or an in-progress assignee, create or assign one;
  - for each due fix-or-retire decision, decide;
  - for each tracker Phase 4 could not create, open it.
  You MUST NOT list your own completed Jira writes as action items.
- You MUST source `outstanding` from the Phase-2/3/4 handoffs (signatures not yet isolated, Phase-3
  evidence gaps, defects left unlinked).
- **Quiet day:** when there is no incident, the `incident` body MUST be exactly `• None: no permafailing
  release or merge-queue blocker this run.`, `action_items` MUST be `• None.`, and `outstanding` MUST be
  `• None.` when there is nothing to carry. You MUST NOT fabricate a borderline incident or add "not
  declared" disclaimers **because** the header already frames it as a recommendation.

### 6. Adversarial self-review (gate before posting)
Before posting, spin up an adversarial reviewer sub-agent and resolve every item it flags.

**Constraints:**
- You MUST run this review and fix anything flagged — re-render the report or the message as needed —
  **before** posting.
- Give the reviewer this checklist:
  - **Formatting matches the committed template** — super-header, section headers, divider bars, bullet
    hierarchy, bold/code/link conventions; no `#`, no `**`, no leftover `$placeholder`; no `@@` keys or
    `<--` notes leaked.
  - The recommended incident **exactly matches the incident-eligible set** (no over/under-recommend).
  - The HTML `incident` annotation and the Slack `@@ incident` body **agree** (same releases/counts/days).
  - **Open the rendered `report.html` and confirm the recommended incident is visible** — a red incident
    callout, not the green all-clear — **because** an incident driven only by a Phase-2 break or the
    merge-queue-handoff case must still show in the management report.
  - Every `job_notes` key is a real `job_id` present in `data.json`; `summary` is present.
  - **Management tone** — no bot self-talk, no internal jargon, no raw job IDs in prose; the report is
    attached.

## Writing the report narrative (annotations)

The attached HTML report is **read by engineering management**, not only RITS/IC. Write every free-form
field for that audience: plain, factual, about the **CI situation** — never about you or your process.
All three fields: `{"summary": "...", "incident": "...", "job_notes": {"<job_id>": "..."}}` (`summary`
required; `incident` required when an incident exists; `job_notes` optional).

- **`summary`** — 2–4 sentences, the day's CI-health headline: how many release-payload and merge-queue
  blockers and on which releases, then the trend and SLO picture (counts from `at_a_glance`).
- **`incident`** — the recommended incident's substance in plain language: which releases/branches are
  blocked, how long, and the impact.
- **`job_notes`** — keyed by `job_id`; one plain line each: suspected cause and next step or owner.

In **every** field you MUST NOT:
- **talk about yourself or your process** — no *"I checked"*, *"I will open"*, *"no incident was
  declared"*, *"no external state was changed"*, *"annotations"*, *"data document"*;
- **use internal jargon** — no *"Axis A/B"*, *"permafail threshold"*, *"candidate"*, phase numbers, tool
  names, or raw job IDs written into prose;
- **restate a section header** or re-dump numbers already shown in the tables.

## Message format

Author the section bodies into `$RUN_DIR/phase-5-bodies.txt` (sections separated by `@@ <key>`), then
render, post **verbatim**, and attach `$RUN_DIR/report.html`:

```text
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py render-message \
  --phase 5 --bodies "$RUN_DIR/phase-5-bodies.txt" --out "$RUN_DIR/phase-5-message.txt"
```

The template owns the `:dart: Health Report Conclusion` super-header, the `:rotating_light: Incident —
recommended for the team` / `:done-circle-check: Team Action Items` / `:warning: Outstanding tracking and
evidence` headers, the `Full HTML report attached. Snapshot: <title_time>.` line, the bars, and the
`(part 5/5)` footer.

**Sections** (one `@@ <key>` each, all required): `title_time` (`<DD Mon, HH:MM UTC>`), `incident`,
`action_items`, `outstanding`. Lead each action item with its owner in bold (`*Human:*`, `*RITS:*`).

### Example `phase-5-bodies.txt`
This is the real file. The `<--` notes explain the Slack formatting mechanic — **do not put them in the
actual file.**

```
@@ title_time
7 Oct, 19:38 UTC
@@ incident
Two gates have been persistently failing for more than two days. Recommend one incident covering both:
• *OCP 4.21 — release-payload gate:* `e2e-aks-multi-x-ax`, 17 consecutive failures (~17%), ~3 days red; last accepted [4.21.0-0.nightly-multi-2026-10-02-115050](https://multi.ocp.releases.ci.openshift.org/releasestream/4.21.0-0.nightly-multi/release/4.21.0-0.nightly-multi-2026-10-02-115050).   <-- bold lead via * *; job in code; tag as a link
• *OCP 4.22 — component-readiness gate:* `e2e-v2-aws`, 27 consecutive failures (0%), ~7 days red.
@@ action_items
• *Human:* declare and handle one incident covering these two gates.   <-- bold owner via * *
• *RITS:* open the missing release-blocker and periodics-health tracking under [CNTRLPLANE-4605](https://issues.redhat.com/browse/CNTRLPLANE-4605) and assign owners.
• *Human:* engage an Azure/AKS owner to investigate the 4.21 gate from its failing-run logs.
@@ outstanding
• Two release-blocker signatures are not yet isolated — capture them before linking defects.
• Of 37 below-SLO periodics, 20 have no in-window runs and 13 lack an isolated signature; the report does not imply a complete diagnosis.
```

## Output: close the run

**Constraints:**
- Append a final dated entry to `$RUN_DIR/journal.md` noting the recommended incident, the action items,
  and the report path.

## Definition of done
`phase-5-message.txt` posted verbatim and `report.html` attached; the incident matches the eligible set
and agrees between the report and Slack; `summary` present; the `journal.md` entry appended; the
adversarial review passed with no open items.
