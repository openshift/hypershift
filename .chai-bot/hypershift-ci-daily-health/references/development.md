# HyperShift CI daily health companion

`hypershift-ci-daily-health.py` supplies the deterministic **data and charts** behind
`.chai-bot/hypershift-ci-daily-health/hypershift_ci_daily_health_report.md`. It uses only the Python standard library
in production and never writes Slack, touches Jira, triggers CI, or mutates a cluster —
**chaibot** composes the Slack messages, triages presubmit candidates, and performs the Jira
reconciliation; the companion is its deterministic tool.

## Layout & single source of truth

The skill lives under `.chai-bot/hypershift-ci-daily-health/`, with the prompt
(`hypershift_ci_daily_health_report.md`) at the root and:

- `scripts/` — the companion `hypershift-ci-daily-health.py`, its offline tests +
  `testdata/`, and `config.toml`.
- `scripts/config.toml` — **the single source of truth for every knob**, read at startup via
  stdlib `tomllib` and validated (a missing/mistyped key fails fast with a located error).
  Knobs are **split by owner**: the script reads `[sources]`, `[scope]`, `[presubmit]`,
  `[periodic]`, `[trend]`, `[job_health].slo_pass_rate_percent`, and `[limits]`; the
  `[incident]` knobs, `[job_health].fix_or_retire_horizon_days` and `progress_window_days`, and
  `[jira]` (incl. `story_project`/`defect_project`) are **chaibot's** (read from `config.toml` by the
  prompt, not by the Jira-blind script).
- `assets/` — the editable HTML report + inline-SVG chart template (`report.html.tmpl`),
  filled with `string.Template`. The Slack text is composed by chaibot, not templated here.
- `references/` — the per-phase workflow+format files (`phase-1-parent-report`,
  `phase-2-candidate-triage`, `phase-3-periodics-health`, `phase-4-jira-bookkeeping`,
  `phase-5-incident-and-report`) plus long-form docs (data-sources, classification, jira-model,
  diagnostic-hints, and this development guide).

## Data sources — Sippy is the health source of truth

The companion computes the 1w health window itself via `Collector._build_health`: registry → plan →
Sippy observation → evaluate → a `health-report/v2` envelope the rest of `collect()` consumes unchanged.

- **CI Health dashboard** `GET /api/job-registry` (`job-registry/v7`): the static job
  **inventory** only (names, type, `e2e_framework`, `required`, `sippy_ingestion`,
  release-controller participations + roles, prow URLs). The dashboard's own `/windows/*` health
  endpoint is **not** fetched.
- **Sippy** (`/api/jobs/analysis` per selected job, `/api/jobs` per release for
  component-readiness membership, `/api/tests/recent_failures` for flake alerts): the health
  numbers — `rate`/`prev`/`trend`/`runs` and the 28×6h sparkline, computed from hourly
  `by_period`. Scope is the N‑4 window computed by `releaseRank` around the dev release.
- **Prow** job-history for presubmit run order (and periodic order when needed).
- **Release controller** — payload **status** context per gated stream: tags (newest phase +
  last-accepted) and the newest tag's per-job blocking verification states (for "is this blocker
  currently holding up a payload" + override detection). Never an Axis-A/B driver.

## Classification — two axes

- **Axis A — permafailing** (a failing state → blockers + the incident set). Presubmits:
  streak `r` = reds since the last SUCCESS from Prow → `not_flagged` (r≤S) / `candidate`
  (S<r, LLM resolves) / `permafailing` (r≥V and span≥D; the fetch window extends past D so
  that span is reachable). Periodics: **binary** from the sparkline — failures since the last
  pass ≥ `permafail_volume` and span ≥ `permafail_duration_hours` ⇒ `permafailing`, else
  `not_permafailing`, else `unknown` (null/mismatched slots).
- **Axis B — SLO + trend.** `rate` (= passes/total, infra counted) below
  `slo_pass_rate_percent` ⇒ tracked (fix-or-retire); week-over-week trend is
  Wilson-confidence-aware. "Healthy" means only "meeting its SLO".
- **flaky** is per-**test** (from `alerts`), never a job class. Gaps ⇒ `Unknown`, never green.

## collect

```console
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py collect \
  --as-of 2026-09-28T12:00:00Z --source-revision "$(git rev-parse HEAD)" \
  --data-out /tmp/data.json --html-out /tmp/trends.html
```

Writes the deterministic **data document** JSON — `at_a_glance` (the scoreboard counts Phase 1 and
Phase 5 read), `scope`, `presubmits`, `presubmit_candidates` (each with `red_runs` and `red_days_met`),
`periodics`, `incident_set` — permafailing release/merge-queue blockers, the release blockers
**enriched** with `rate`/`streak_runs`/`span_hours`/payload so Phase 1 renders and Phase 4 reads them
with no join (the merge-queue branch-blocked-since still comes from Jira) — `flaky_tests`, and
`job_health_below_slo` (with `prev`) — and, optionally, the rich HTML report (see `report` below;
rendered without annotations when produced here).

## report

```console
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py report \
  --data /tmp/data.json [--annotations /tmp/annotations.json] \
  --html-out /tmp/report.html
```

Renders the **rich HTML report** from a data document: per-release cards with pass-rate
bars, status pills, payload phase, and collapsible per-periodic trend charts, plus presubmit
and flaky-test tables and a recommended-incident callout. The layout is fixed and deterministic;
the only free-form content is chaibot's optional `--annotations` JSON —
`{"summary": …, "incident": …, "job_notes": {"<job_id>": …}}` (an overall summary, the
incident narrative, and per-job notes), bounded and HTML-escaped on render.

## Offline verification

Committed fixtures (`scripts/testdata/hypershift_ci_daily_health/`) model the real sources the
builder consumes: a `job-registry.json` inventory (ingested/non-ingested presubmits, blocking +
component-readiness-only periodics with participations/roles, an out-of-scope and an informing
periodic that must be dropped), per-job Sippy `by_period` analyses that bin into the expected
rates/sparklines (permafail streaks, healthy, below-SLO, a present-empty vs an **absent/failed**
analysis that reads `Unknown`), per-release component-readiness membership, recent-failure
alerts, and the release-controller tag/release reads. Tests cover the new transform helpers
(`release_rank`/inverse, `sippy_display_name`, `spark_slot_keys`, `summarize_analysis`,
`analysis_sparkline` empty-vs-absent, `build_sippy_alerts`, `_select_plan` filters + window), the
classifiers and fail-safe `Unknown` paths, scope, both-section blocker enumeration, the incident
set, below-SLO exclusions, a config knob flowing through to an outcome, document validation, the
`collect`/`report` CLI round-trip, the registry-failure fail-closed path, and the presubmit
fetch-window regression.

```console
make verify-hypershift-ci-daily-health
```

## Roadmap / known gaps

- **v1 vs v2 framework split.** The report treats each job individually; it does not yet roll a
  platform up into its old-framework (`v1`) vs new-framework (`v2`) health the way the legacy
  report did (e.g. "AWS v1 55% / v2 81%"). Tracked as future work.
