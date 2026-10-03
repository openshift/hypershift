# Jira & incident model

**Jira is the only living, persistent artifact.** The daily data document the companion
writes is ephemeral — you cannot fetch yesterday's — so every cross-day fact (a blocker's
blocked-since, the fix-or-retire clock, whether something is already tracked) is read from
Jira, fresh, each run. You MUST perform all Jira through your own Jira tools in a **propose →
approve → execute** loop: you MUST search first, propose in the thread, and MUST execute the
writes only after human approval. Every issue MUST be labelled with `[jira].label` on the
`[jira].component` from `config.toml` (currently `rits-work` / Hosted Control Plane).

## Issue hierarchy (CNTRLPLANE is the tracking spine)

All tracking lives on **CNTRLPLANE**; OCPBUGS are linked **defect** records, never the spine.

```
Epic (per dev cycle) → Story (per release / per branch) → Sub-task (per job) → linked OCPBUGS (per defect)
```

- **Epic — one per dev cycle** (the OCP release under development, ~3–4 months). Every
  top-level story below lives under the **current dev-cycle epic**; you MUST find it, or open
  it, first.
- **Story — CNTRLPLANE, per release or per branch**, four kinds, all under the epic:
  - **release-blocker**, one **per release** — permafailing payload-blocking / component-readiness periodics.
  - **merge-queue**, one **per branch** — permafailing required presubmits. Its open episode
    *is* the branch-blocked span that drives the incident.
  - **flake**, one **per dev cycle** (under the epic) — a sub-task **per flaky test** (from
    `alerts`), ranked by a **weight**; the sub-task lists the jobs/branches where it flakes. No incident.
  - **periodics-health**, one **per release** — **periodics** below the SLO (Axis B) that are
    **not** already flagged as release blockers; a sub-task per periodic, lifecycle-managed
    (open → nudge → fix-or-retire → close). Presubmit SLO health is out of scope for now.
- **Sub-task — CNTRLPLANE, one per job**, under its story: one sub-task per permafailing
  periodic job (release-blocker), per permafailing presubmit job (merge-queue), per flaky test
  (flake), or per below-SLO periodic (periodics-health). Created **unassigned** — RITS assigns via the
  thread; act as a nudge (surface a high-priority sub-task open 1–2 days with no assignee /
  no progress in Team Action Items).
- **OCPBUGS — one per distinct defect**: the defect record (symptom, verified signature,
  evidence, component). Each is **linked from the sub-task(s)** of the job(s) it affects. A
  job with several defects → its sub-task links several OCPBUGS; one defect hitting several
  jobs → **one** OCPBUGS linked from each of those jobs' sub-tasks (dedup by defect).

Relationships: 1 release → n periodic jobs, each job → m defects; 1 branch → n presubmit jobs,
each job → m defects. So 1 release → 1 release-blocker story → n sub-tasks (per permafailing
periodic) → each linked to m OCPBUGS; 1 branch → 1 merge-queue story → n sub-tasks (per
permafailing presubmit) → each linked to m OCPBUGS.

## Actions by signal

- **Permafailing required presubmit, or permafailing payload-blocking / component-readiness
  periodic:** ensure the per-branch / per-release CNTRLPLANE story (under the dev-cycle epic),
  a CNTRLPLANE **sub-task for that job** under it, and an **OCPBUGS defect linked from the
  sub-task** per distinct defect (dedup a shared defect across jobs); concise thread line +
  issue links; include in the proposed incident per the criteria below.
- **Presubmit candidate you resolve to a real break:** same as permafailing above.
- **Presubmit candidate → flaky test(s):** per test → CNTRLPLANE sub-task under the
  **per-dev-cycle** flake story, with an OCPBUGS flake bug linked (dedup by test); the sub-task
  lists jobs+branches + a weight. No incident.
- **Presubmit candidate → false alarm:** no writes; one-line note. (Nothing is "recorded"
  in the ephemeral data doc; any suppression that must persist lives in Jira.)
- **Below SLO (Axis B) — periodics:** a **periodics-health** sub-task under the per-release story
  (Phase 3), diff-driven: **open** when newly breached (link the OCPBUGS cause), **nudge** when
  deteriorating, **escalate** to fix-or-retire once aged past
  `[job_health].fix_or_retire_horizon_days` with no progress, and **close** when recovered above
  SLO. Excludes periodics already flagged as release blockers; presubmit SLO health is out of scope.

## Incident criteria (one incident covering all blockers; you only propose)

Propose a single incident (covering all blocked releases/branches) for any **permafailing** blocker:
- A permafailing payload-blocking / component-readiness periodic, or a permafailing required
  presubmit, is **already ≥ 2 days red by construction** — the permafail class requires the
  streak to span ≥ the 2-day duration. It is incident-eligible on its own; the companion's
  `incident_set` lists exactly these. **You MUST NOT require a pre-existing Jira story to
  propose** — look up the tracking story if it exists, else open one after approval.
- The **story's creation time** is the duration signal only for the **merge-queue handoff**
  case: a branch that stays blocked > `[incident].blocked_days_threshold` days while
  individual required presubmits each last < 2 days (none is permafailing, but the branch
  never clears). There, blocked-since = `now − the unresolved per-branch story's creation time`.
- A blocking job red **< 2 days** is not yet incident-eligible, but MUST have an active
  major/critical OCPBUGS + an assigned in-progress task, and you MUST surface it if missing.

Declaring the incident, the bridge, and situational awareness are human — never yours.

TODO (later): set flake subtask priority from the flake rate; weight aggregated-job trend
confidence.
