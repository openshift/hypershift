# HyperShift CI daily health companion

`hypershift-ci-daily-health.py` supplies the deterministic **data and charts** behind
`.chai-bot/hypershift_ci_daily_health_report.md`. It uses only the Python standard library
in production and never writes Slack, touches Jira, triggers CI, or mutates a cluster —
**chaibot** composes the Slack messages, triages presubmit candidates, and performs the
approval-gated Jira work; the companion is its deterministic tool.

## Layout & single source of truth

Everything tunable lives under `hypershift-ci-daily-health/`:

- `config.toml` — **the single source of truth for every knob**, read at startup via stdlib
  `tomllib` and validated (a missing/mistyped key fails fast with a located error). Knobs are
  **split by owner**: the script reads `[sources]`, `[scope]`, `[presubmit]`, `[periodic]`,
  `[trend]`, `[job_health].slo_pass_rate_percent`, and `[limits]`; the `[incident]` knobs,
  `[job_health].fix_or_retire_horizon_days`, and `[jira]` are **chaibot's** (read from
  `config.toml` by the prompt, not by the Jira-blind script).
- `assets/` — the editable HTML report + inline-SVG chart template (`report.html.tmpl`),
  filled with `string.Template`. The Slack text is composed by chaibot, not templated here.
- `references/` — long-form docs the prompt loads on demand (data-sources, classification,
  jira-model, diagnostic-hints, reader-guide).

## Data sources — the dashboard is the source of truth

- **CI Health dashboard** `GET /_dashboard/health/windows/1w` (`health-report/v2`): an
  envelope whose top-level `releases` array gives the **scope** (last
  `supported_predecessors + 1` = N‑4) and whose `data` has **four** sections — `jobs`
  (presubmits, nested `periodics[]` are only a human-mapped subset), `payload_blocking_jobs`
  and `component_readiness_jobs` (the **authoritative** blocking periodics, health inline),
  and `alerts` (per-test flake signals).
- **Prow** job-history for presubmit run order (and periodic order when needed).
- **Release controller** — consulted only for the live payload **phase** (context).

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
python3 hack/ci/hypershift-ci-daily-health.py collect \
  --as-of 2026-09-28T12:00:00Z --source-revision "$(git rev-parse HEAD)" \
  --data-out /tmp/hcih-data.json --html-out /tmp/hcih-trends.html
```

Writes the deterministic **data document** JSON — `scope`, `presubmits`,
`presubmit_candidates`, `periodics`, `incident_set` (permafailing release/merge-queue
blockers, **no duration** — chaibot computes blocked-since from Jira), `flaky_tests`, and
`job_health_below_slo` — and, optionally, the HTML trend report (one inline-SVG pass-rate
chart per blocking periodic, grouped by release).

## report

```console
python3 hack/ci/hypershift-ci-daily-health.py report \
  --data /tmp/hcih-data.json --html-out /tmp/hcih-trends.html
```

Re-renders the HTML trend report from a previously written data document.

## Offline verification

Committed fixtures (`testdata/hypershift_ci_daily_health/`) model the real four-section
envelope (top-level `releases`, a 28×6h sparkline, payload-blocking and
component-readiness periodics that are *not* in the nested subset, a recent-pass periodic,
a null/unknown periodic, and an out-of-scope release). Tests cover the classifiers (incl.
the permafail examples and the fail-safe `Unknown` paths), scope-from-`releases`,
both-section blocker enumeration, the incident set, below-SLO exclusions, a config knob
flowing through to an outcome, document validation, the `collect`/`report` CLI round-trip,
and the presubmit fetch-window regression.

```console
make verify-hypershift-ci-daily-health
```
