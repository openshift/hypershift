# Classification decision tree

Every job is judged on **two independent axes**. "Healthy" means only "meeting its SLO";
"flaky" is a per-**test** concept, never a job class. All thresholds are named constants in
`config.toml` (defaults in parentheses).

## Axis A — permafailing (a failing state → blockers & incidents)

### Presubmits (deterministic + your triage)

`required: true` ⇒ the job gates that branch's merge queue. Let `r` = consecutive red
(FAILURE) runs since the last SUCCESS from the ordered Prow history; `ERROR`/`ABORTED` are
**neutral** (skipped, counted as an infra/flake tax). The lookback extends past
`permafail_duration_hours` so a `≥ D` span is observable.

| Condition | Class | Who decides |
|---|---|---|
| no testable (SUCCESS/FAILURE) runs | `unknown` | companion |
| `r ≤ S` (post_green_red_tolerance, 3) | `not_flagged` — a recent green | companion |
| `r > S` and (`r < V` or span `< D`) | `candidate` | companion flags → **you** resolve |
| `r ≥ V` (permafail_volume, 60) and span `≥ D` (permafail_duration_hours, 48h) | `permafailing` — merge-queue blocker | companion |

Streak is **run-based, not wall-clock**, so it self-normalizes to branch traffic.
Candidates are shaded by **H** (flake_horizon_hours, 24h): span `< H` = "fresh"; `≥ H` =
"persistent". You MUST resolve each candidate into a **real break**, **flaky**, **false
alarm** (per-PR bad code / infra / already recovered), or **inconclusive** (logs unreadable —
fail closed, never default to false alarm), citing public run evidence; a real-break verdict
MUST have `≥ 3` reds across `≥ 2` PR heads sharing one actionable signature.

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
