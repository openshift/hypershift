# Classification decision tree

Every job is judged on **two independent axes**. "Healthy" means only "meeting its SLO";
"flaky" is a per-**test** concept, never a job class. All thresholds are named constants in
`config.toml` (defaults in parentheses).

## Axis A — permafailing (a failing state → blockers & incidents)

### Presubmits (deterministic elimination funnel + your triage)

`required: true` ⇒ the job gates that branch's merge queue. From the ordered Prow history
(SUCCESS/FAILURE; `ERROR`/`ABORTED` are **neutral**, skipped as an infra/flake tax) the companion runs an
**elimination funnel** over the job's **organic** runs (no-op probe runs are handled separately, below).
Recency is counted in **business days** (presubmits barely run on weekends). `f` = **distinct failing
PRs** — counted by PR number, so reruns and extra commits on one PR count once; `T` = `min_failing_prs`
(3). The first gate that matches wins:

| Gate | Condition | Class | Who decides |
|---|---|---|---|
| G0 | last testable run older than `stale_business_days` (2 bd), or none | `under-observed` | companion |
| G1 | last run SUCCESS, or a SUCCESS within `green_recency_hours` (24h) | `eliminated` | companion |
| G2 | `f ≤ 1` (a single bad PR, not a branch break) | `eliminated` | companion |
| G3 | `f ≥ T` **and** red span since the last pass `≥ D` (`permafail_duration_hours`, 48h) | `permafailing` — merge-queue blocker | companion |
| G4 | low throughput → `candidate`; high + overwhelming failing share (`≥ overwhelm_rate`) → `candidate`; high + sparse → `eliminated` | `candidate` / `eliminated` | companion flags → **you** resolve candidates |

**Throughput** is a per-branch property measured over a fixed `throughput_window_business_days` (10 bd)
window via the branch's most-active required job; a branch is "high" when it produces `≥ HI` PRs/bd, where
`HI = T ÷ report_interval` (derived, not hand-set). Only `under-observed`, `candidate`, and `permafailing`
are reported — `eliminated` is dropped (there is no "green" output).

**No-op probe path.** When G0 would mark a job `under-observed`, a **fresh** no-op "health-probe" PR run
(identified by the `[presubmit.probe]` marker) classifies it directly: the probe carries no real code, so
a failure on it is attribution-clean (the job/branch is broken) and a single red is enough — the
distinct-PR gates (G2 and the `f ≥ T` half of G3) are bypassed, while recency and the `≥ D` sustained-span
still apply. Probe recent pass → `eliminated`; sustained probe red (span `≥ D` or ≥ 2 probe reds) →
`permafailing`; a single probe red → `candidate`; a stale probe → stays `under-observed`.

You MUST resolve each `candidate` into a **real break**, **flaky**, **false alarm** (per-PR bad code /
infra / already recovered), or **inconclusive** (logs unreadable — fail closed, never default to false
alarm), citing public run evidence; a real-break verdict MUST have `≥ 2` distinct failing PRs (`f`)
sharing one actionable signature.

### Periodics (deterministic, binary)

No PR-head ambiguity, so periodics are **binary** and MUST NOT enter your judgment. From the
ordered run list (the Sippy-derived sparkline, ~1 run per 6h slot; Prow when exact order is
needed), count failures **since the last pass**:

- **`permafailing`** — failures since the last pass `≥ V_periodic` (permafail_volume, 4)
  **and** the streak spans `≥ permafail_duration_hours` (48h).
- **`not_permafailing`** — otherwise.
- **`unknown`** — null/mismatched slots, no run data.

A recent pass does not by itself clear it — an *old* pass followed by enough failures over
enough time is permafailing. The **blocking** periodics are enumerated from
`payload_blocking_jobs` ∪ `component_readiness_jobs`; a permafailing member is a release
blocker. Live payload phase is context and MUST NOT drive the classification.

"Binary" means the *classification* is the companion's, never yours. You still open a
below-SLO periodic's run log to keep its Jira **tracking** faithful — is the breach a real current
failure, and does the linked OCPBUGS still describe it? — which verifies the record without
re-classifying the verdict.

### Why the incident keys off permafailing, not pass-rate

The recommended incident is driven by **Axis A (permafailing)** — a job *stuck* failing ≥ 2 days —
not by a low pass-rate. (A human-confirmed **real break** red ≥ 2 days counts too, though a
candidate is below the permafail bar by construction — see `jira-model.md` incident criteria.) A rate-based net (e.g. "everything below 50%") was deliberately avoided:
it floods the recommendation with jobs the team already knows are flaky or slowly recovering, so it
stops matching their situational awareness and gets ignored. Blockers are reported as the
**individual failing job**, never a platform/category average — an aggregate like "AWS v1 55%"
hides the one periodic that is permafailing and blocking the release.

## Axis B — SLO + trend (health / deterioration)

Applies to **every** job (periodic or presubmit, blocking or not), independent of Axis A.

- **SLO:** `rate` (= passes/total, infra counted) `< slo_pass_rate_percent` (80%) ⇒
  **below SLO** ⇒ tracked for restoration (periodics → the per-release periodics-health story;
  fix-or-retire). `rate ≥ SLO` = meeting its SLO
  = "healthy". Sustained below SLO past `fix_or_retire_horizon_days` (21, chaibot's knob,
  measured from the Jira subtask age) with no progress becomes a "fix or retire" decision.
- **Trend:** week-over-week `rate` vs `prev`, **confidence-aware**: either window `< N_min`
  (min_window_runs, 8) runs ⇒ insufficient; else compare the two weekly Wilson 95%
  intervals — improving/degrading only when disjoint **and** `|rate − prev|` ≥
  `change_threshold_points` (10pp); else stable.

The axes are independent: a job can be `not_permafailing` yet below SLO (tracked, no
incident), or meeting its SLO with a degrading trend (early warning).
