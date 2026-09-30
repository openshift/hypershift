# Jira & incident model

**Jira is the only living, persistent artifact.** The daily data document the companion
writes is ephemeral — you cannot fetch yesterday's — so every cross-day fact (a blocker's
blocked-since, the fix-or-retire clock, whether something is already tracked) is read from
Jira, fresh, each run. You perform all Jira through your own Jira tools in a **propose →
approve → execute** loop: search first, propose in the thread, and only after human
approval execute the writes. Everything is labelled with `[jira].label` on the
`[jira].component` from `config.toml` (currently `rits-work` / Hosted Control Plane).

## Four stories (granularity mirrors the domain)

- **Per-release release-blocker story** — one CNTRLPLANE subtask per **permafailing**
  payload-blocking **or** component-readiness periodic.
- **Per-branch merge-queue story** — one subtask per **permafailing** required presubmit.
  The story's open episode *is* the branch-blocked span that drives the incident.
- **Per-release flake story** — one subtask per flaky **test** (from `alerts`), ranked by a
  **weight**; the subtask lists every job + branch where the test flakes. No incident.
- **Per-release job-health story** — one subtask per job **below the SLO** (Axis B;
  fix-or-retire), including non-blocking degraded jobs.

## Bug vs subtask

- **OCPBUGS bug = the defect**: symptom, verified signature, evidence, component.
- **CNTRLPLANE subtask = the next action**: revert / CPO override / fix / route.

One defect → one bug + successive subtasks, created **unassigned** — RITS assigns via the
thread. Act as a nudge: surface a high-priority task open 1–2 days with no assignee / no
progress in the Action Items.

## Actions by signal

- **Permafailing required presubmit, or permafailing payload-blocking / component-readiness
  periodic:** OCPBUGS defect bug + CNTRLPLANE subtask under the per-branch / per-release
  story; concise thread line + issue links; include in the proposed incident per the
  criteria below.
- **Presubmit candidate you resolve to a real break:** same as permafailing above.
- **Presubmit candidate → flaky test(s):** per test → OCPBUGS flake bug (dedup by test) +
  subtask under the per-release flake story; description lists jobs+branches + a weight. No
  incident.
- **Presubmit candidate → false alarm:** no writes; one-line note. (Nothing is "recorded"
  in the ephemeral data doc; any suppression that must persist lives in Jira.)
- **Below SLO (Axis B):** subtask under the per-release job-health story; escalate to
  fix-or-retire once that subtask has aged past `[job_health].fix_or_retire_horizon_days`.

## Incident criteria (one umbrella jobs-incident; you only propose)

Propose the single jobs-incident when a **branch** (≥ 1 permafailing required presubmit) OR
a **release** (≥ 1 permafailing payload-blocking or component-readiness periodic) has been
**continuously blocked > `[incident].blocked_days_threshold` (2) days** — measured on the
dimension, not the job, so handoffs that keep it blocked still count. **The companion does
not compute duration**; it emits only the deterministic permafailing set (`incident_set`).
**You** compute blocked-since as `now − the unresolved per-branch/per-release story's
creation time` and decide incident-worthiness. **Blocked ≤ 2 days:** not incident-eligible,
but each blocking job must have an active major/critical OCPBUGS + assigned in-progress
task, else surface it. Declaring, bridging, and situational awareness are human — never
yours.

TODO (later): set flake subtask priority from the flake rate; weight aggregated-job trend
confidence.
