# Jira & incident model

**Jira is the only living, persistent artifact.** The daily data document the companion
writes is ephemeral — you cannot fetch yesterday's — so every cross-day fact (a blocker's
blocked-since, the fix-or-retire clock, whether something is already tracked) is read from
Jira, fresh, each run. You MUST perform all Jira through your own Jira tools as a
**reconciliation**: search first, then create what's missing, update what drifted, and close what's
resolved — directly, to make Jira match today's reality. Every **CNTRLPLANE** issue you create (epic, story, sub-task)
MUST be labelled with `[jira].label` on the `[jira].component` from `config.toml` (currently
`rits-work` / Hosted Control Plane); the OCPSTRAT anchor uses its own label (below). Throughout,
**CNTRLPLANE** is `[jira].story_project` and **OCPBUGS** is `[jira].defect_project` — the project
names are config knobs, cited by those keys.

## Issue hierarchy (CNTRLPLANE is the tracking spine)

All tracking lives on **CNTRLPLANE**; OCPBUGS are linked **defect** records, never the spine.
The whole spine hangs under a per-release **OCPSTRAT Feature** (the strategic anchor, below).

```
OCPSTRAT Feature (per release) → Epic (per dev cycle) → Story (per release / per branch) → Sub-task (per job) → linked OCPBUGS (per defect)
```

- **OCPSTRAT Feature — the strategic anchor, one per release** (human-owned; broader than CI —
  Konflux, region onboarding, release process). Example: OCPSTRAT-3645 *"Release and Pipeline
  Maintenance for Hosted Control Planes (5.1)"*. **Find it by label:** an open `Feature` in
  `[jira].release_tracker_project` (OCPSTRAT) carrying `[jira].release_tracker_label`
  (`hcp-ci-release-tracker`) that matches the current dev release — the label, not the component,
  locates it. **If the dev release has none, presume it absent and create one** from the
  create-template below. You create/edit only the
  CI-daily-health epic and below; the OCPSTRAT's non-CI content is human-owned.
- **Epic — CNTRLPLANE, one per dev cycle, under the release's OCPSTRAT.** Find or create it **by
  naming convention**: the epic whose title starts with `[jira].epic_title_prefix`
  (`HyperShift CI Daily Health`) followed by the dev release, e.g. *"HyperShift CI Daily Health —
  5.1"* — this distinguishes our epic from the OCPSTRAT's other (non-CI) epics. Every top-level
  story below lives under it; find or open it first.
- **Story — CNTRLPLANE, per release or per branch**, four kinds, all under the epic:
  - **release-blocker**, one **per release** — permafailing payload-blocking / component-readiness periodics.
  - **merge-queue**, one **per branch** — permafailing required presubmits. Its open episode
    *is* the branch-blocked span that drives the incident.
  - **flake**, one **per dev cycle** (under the epic) — a sub-task **per flaky test**, sourced from
    `data.json` `flaky_tests[]` (the companion's per-test alerts) and folding in any flaky verdict reached by triaging a
    presubmit candidate. Its identity is the verbatim **`test_name`** (which MUST appear in the sub-task summary,
    matched like a job sub-task's `job_id`); the volatile jobs/branches list lives in the description
    only. No incident. (A per-test weight/priority is a future enhancement — do not set or fabricate one.)
  - **periodics-health**, one **per release** — **periodics** below the SLO (Axis B) that are
    **not** already flagged as release blockers; a sub-task per periodic, lifecycle-managed
    (open → nudge → fix-or-retire → close). Presubmit SLO health is out of scope for now.
- **Sub-task — CNTRLPLANE, one per job**, under its story: one sub-task per permafailing
  periodic job (release-blocker), per permafailing presubmit job (merge-queue), per flaky test
  (flake), or per below-SLO periodic (periodics-health). Created **unassigned** — RITS assigns via the
  thread; act as a nudge (surface a high-priority sub-task open 1–2 days with no assignee /
  no progress in Team Action Items). An **SLO tracker** sub-task (every periodics-health sub-task, and
  a release-blocker's per-job sub-task) **MUST record the observed SLI** — the job's pass `rate` and
  the **observation time** (the run's `--as-of`, RFC3339, = the data document's `generated_at`) — in
  **both** its summary and its description, e.g. summary `Restore OCP 4.22 e2e-aws-ovn-conformance
  periodic health (52% @ 2026-10-07T19:38:36Z)`, so the point-in-time SLI stays auditable across days.
  On **update** (the sub-task already exists), **replace** the SLI in the summary with the new reading,
  but **append** the new observation (`<rate>% @ <RFC3339>`) to a running list in the description —
  never overwriting prior ones — so the description accrues the SLI history run over run. The append
  is **idempotent**: add the observation **only if** that exact `@ <RFC3339>` is not already in the
  description, so a re-run against the same data document does not double-count.
- **OCPBUGS — one per distinct defect**: the defect record (symptom, verified signature,
  evidence, component). Each is **linked from the sub-task(s)** of the job(s) it affects. A
  job with several defects → its sub-task links several OCPBUGS; one defect hitting several
  jobs → **one** OCPBUGS linked from each of those jobs' sub-tasks (dedup by defect).

Relationships: 1 release → n periodic jobs, each job → m defects; 1 branch → n presubmit jobs,
each job → m defects. So 1 release → 1 release-blocker story → n sub-tasks (per permafailing
periodic) → each linked to m OCPBUGS; 1 branch → 1 merge-queue story → n sub-tasks (per
permafailing presubmit) → each linked to m OCPBUGS.

### Creating the OCPSTRAT anchor (when absent)

Create a new per-release OCPSTRAT `Feature`, seeded from OCPSTRAT-3645:
- **project** `OCPSTRAT`, **issue type** `Feature`, **component** `Hosted Control Planes`;
- **summary** `Release and Pipeline Maintenance for Hosted Control Planes (<dev release>)`;
- **labels** `hcp-ci-release-tracker`, `control-plane-work`, `no_core_payload`;
- **description** mirroring OCPSTRAT-3645's sections — Market Problem · Proposed Solution ·
  Strategic Value · Success Criteria · Scope · Dependencies · Risks · Target Users — with the
  dev release substituted, closing with an *"AI-generated. Review for accuracy."* note.

### OCPBUGS invariants

- **One OCPBUGS tracks exactly one defect for its entire life.** You MAY update it with findings,
  progress, evidence, or a false-alarm conclusion, and you MAY **close** it when the defect is
  fixed, gone, a false alarm, flaky, or obsolete. You MUST NOT **re-point** an OCPBUGS at a
  *different* defect — a new/different defect gets a **new** OCPBUGS.
- **No job / SLO / health state is ever recorded on an OCPBUGS.** A job's health, its SLO breach,
  its rate/trend, and its recovery live **only** on the job's CNTRLPLANE sub-task; the SLO number
  itself lives in the HTML / data document, never in Jira. An OCPBUGS records a *defect* — never
  "job X is below SLO".
- A defect spreading to more jobs = the **same** OCPBUGS (update its findings, link it from each
  new job's sub-task) — never a second OCPBUGS, never an edit that changes which defect it is.

## Actions by signal

- **Permafailing required presubmit, or permafailing payload-blocking / component-readiness
  periodic:** ensure the per-branch / per-release CNTRLPLANE story (under the dev-cycle epic),
  a CNTRLPLANE **sub-task for that job** under it, and an **OCPBUGS defect linked from the
  sub-task** per distinct defect (dedup a shared defect across jobs); concise thread line +
  issue links; include in the recommended incident per the criteria below.
- **Presubmit candidate you resolve to a real break:** same as permafailing above.
- **Presubmit candidate → flaky test(s):** per test → CNTRLPLANE sub-task under the
  **per-dev-cycle** flake story, with an OCPBUGS flake bug linked (dedup by test); the sub-task
  lists jobs+branches. No incident. (A weight/priority is a future enhancement — not set yet.)
- **Presubmit candidate → false alarm:** no writes; one-line note. (Nothing is "recorded"
  in the ephemeral data doc; any suppression that must persist lives in Jira.)
- **Presubmit candidate → inconclusive:** no writes; a one-line note that it was not resolvable this
  run (logs unreadable/ambiguous) and will be re-triaged next run.
- **Below SLO (Axis B) — periodics:** a **periodics-health** sub-task under the per-release story,
  diff-driven: **open** when newly breached (link the
  OCPBUGS cause), **nudge** when
  deteriorating, **escalate** to fix-or-retire once aged past
  `[job_health].fix_or_retire_horizon_days` with no progress, and **close** when recovered above
  SLO. Excludes periodics already flagged as release blockers; presubmit SLO health is out of scope.

## Reconciliation

Write all of the above by **search-first reconciliation**: find what already exists, create what
is missing, update what drifted, and close what resolved. When exactly one open issue matches a tier's
convention below, reuse it; **none**, create it; **more than one** is a duplicate — write nothing and
record it (all keys) as an Outstanding gap, never guessing which one, **because** a wrong guess mutates
production Jira.

Identify each tier by:

- **OCPSTRAT Feature** — the open `Feature` in `[jira].release_tracker_project` that carries
  `[jira].release_tracker_label` for the current dev release (the label locates it — see *Issue
  hierarchy*). Creating one is a **last resort** (human-owned) — only when none exists.
- **Epic** — the `Epic` under that OCPSTRAT whose summary is exactly the pinned title
  `<[jira].epic_title_prefix> — <dev release>`.
- **Story** — the **open** story under that epic for this release (or branch) and kind, carrying its
  pinned title. It MUST be the open one: a resolved story is a past, closed episode, so a new block
  episode opens a new story rather than reusing it, **because** writing today's sub-tasks under a resolved
  story buries them in closed history.
- **Sub-task** — under its story, identified by its `job_id` (which appears verbatim in the summary).
- **OCPBUGS** — the existing defect whose **failing test/step AND error signature both match** (scan the
  OCPBUGS already linked from sibling sub-tasks first); if none matches, it is a new defect and gets a new
  OCPBUGS. You MUST NOT reuse a bug whose signature differs, **because** one OCPBUGS tracks one defect for
  life.

**Close the merge-queue story when a branch clears:** once none of a per-branch merge-queue story's
blocking presubmits is still a real break, close the story — this resets the branch-blocked-since clock
that drives the incident (an unclosed story would recommend a phantom incident forever).

Every spine node you create (epic, story, sub-task) gets a **fixed, pinned summary** — the templates
named above — so the next run re-identifies it by the same convention.

**Parentage & links:** set a sub-task's parent to its story at creation; link the epic under the
OCPSTRAT via the Parent Link and stories under the epic via the Epic Link; link an OCPBUGS to a sub-task
with the **"is caused by"** link type (the sub-task is caused by the defect). **Close** via the `Closed`
transition with the resolution valid for that project: a **CNTRLPLANE** sub-task/story uses `Done`
(recovered/fixed) or `Won't Do`/`Obsolete` (false alarm / no longer tracked); an **OCPBUGS** uses `Done`
(fixed) or `Not a Bug` (false alarm). If a required link type, transition, or resolution is unavailable
in the project, STOP and record an Outstanding gap — never substitute one silently.

**Bounded updates:** on an existing issue, update ONLY the SLO-tracker summary/description SLI and the
OCPBUGS link set. You MUST NOT change assignee, priority, or status (except the defined close) — those
are human-owned and a blind update clobbers their edits.

## Incident criteria (one incident covering all blockers; you recommend)

Recommend a single incident (covering all blocked releases/branches) for any **permafailing** blocker:
- A permafailing payload-blocking / component-readiness periodic, or a permafailing required
  presubmit, is **already ≥ 2 days red by construction** — the permafail class requires the
  streak to span ≥ the 2-day duration. It is incident-eligible on its own; the companion's
  `incident_set` lists exactly these. **You MUST NOT require a pre-existing Jira story to
  recommend** — look up the tracking story if it exists, else open one.
- A **human-confirmed real break** (a merge-queue candidate you resolved to a real break) that is
  **red ≥ 2 days** (`red_days_met`) is **also** incident-eligible, even though it is not in the
  companion's `incident_set` (a candidate is below the permafail bar by construction). Mark it
  `incident_eligible` and union it with `incident_set` when recommending the incident.
- The **story's creation time** is the duration signal only for the **merge-queue blocked-branch**
  case: a branch that stays blocked > `[incident].blocked_days_threshold` days while
  individual required presubmits each last < 2 days (none is permafailing, but the branch
  never clears). There, blocked-since = `now − the unresolved per-branch story's creation time`.
- A blocking job red **< 2 days** is not yet incident-eligible, but MUST have an active
  major/critical OCPBUGS + an assigned in-progress task, and you MUST surface it if missing.

TODO (later): set flake subtask priority from the flake rate; weight aggregated-job trend
confidence.
