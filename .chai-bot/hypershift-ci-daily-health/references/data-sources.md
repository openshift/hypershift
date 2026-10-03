# Public data sources and their authority

The companion reads these; you (the bot) MUST only follow the canonical public URLs it
emits. All source URLs live in `config.toml` under `[sources]`. The **dashboard is the
source of truth**; the release controller is consulted only for the live payload phase.

## HyperShift CI Health dashboard — primary source

`GET /_dashboard/health/windows/{1w,2w,1m}` (`health-report/v2`) — the computed health
**envelope**. Its top level carries the canonical **`releases`** array (ascending) plus
`window`, `generated_at`, and `complete`; the per-window health lives under **`data`**,
which has FOUR sections:

- **`data.jobs`** — the **presubmit** rows (already filtered upstream to required, e2e,
  release-targeted), each with `role`/`role_label` (the N-x scope), `target_branch`,
  `target_release`, `rate`/`prev`/`prev_runs`/`trend`/`runs`/`fails`/`test_fails`/
  `infra_fails`, a per-slot `sparkline`, and `prow_job_history_url`. Nested `periodics[]`
  exist but are only the human-curated presubmit↔periodic subset — you MUST NOT use them to
  enumerate blockers.
- **`data.payload_blocking_jobs`** — the **authoritative** set of periodics that block a
  release payload (full health inline: `rate`/`sparkline`/`prow_job_history_url` plus
  `participations[]`, each carrying that stream's `release_status_url`).
- **`data.component_readiness_jobs`** — periodics gating **component readiness** (a second
  release gate). A permafailing one is a release blocker too.
- **`data.alerts`** — per-test failure signals (`test_name`, `failure_count`, `jobs`); the
  source for flaky-**test** tracking.

`data.sparkline_slots` gives the timestamp of each sparkline slot (28×6h for `1w`).
The `/api/job-registry` endpoint still exists for deeper inventory details, but the
companion **no longer fetches it** — inventory, scope, rates, trend, the sparkline, the
blocking sections, and the alerts all come from the health report.

**Scope.** Read the envelope's top-level `releases` (ascending); the newest is the
development release N. Supported scope = the last `scope.supported_predecessors + 1`
entries (N-4 ⇒ 4). You MUST NOT hardcode a release list.

**Rate.** `rate = passes / total_runs` — infra and aborted runs are counted in the
denominator (one number, so a job dragged below the SLO by infra stays visible). The
permafail pattern treats infra as a retest tax, but the SLO uses `rate`.

**Nullability / fail-safe.** Periodic `rate`/`prev`/`trend` may be null and sparkline
slots may be null; a presubmit `rate` of `0` with `sparkline: null` means no Sippy data
(disambiguate via `runs > 0` / `trend != null`). Any such gap MUST be reported as
`Unknown`, never green.

## Release controller — live payload phase only

`amd64.ocp.releases.ci.openshift.org` (and the `multi.` host) give the current payload
phase (`Pending`/`Ready`/`Accepted`/`Rejected`) for a stream. The companion queries them
**only** via the `release_status_url` a blocking periodic advertises in the dashboard's
`participations[]`, to report phase as context. Payload phase is never the
permafail/incident driver — that comes from the deterministic class.

## Sippy — human reference only

The dashboard already aggregates Sippy, so the companion does **not** fetch Sippy. Sippy
links (jobs view, component readiness) are for humans to click.

## Prow — ordered run logs

Public Prow job-history (`prow_job_history_url`, present on presubmit and periodic rows)
provides ordered runs, states, timestamps, PR head SHAs, and run links. The companion uses
these for the run-level permafail streak (presubmits always; periodics when exact run
order is needed); you use the canonical run links it emits to read logs when triaging a
candidate. Prow establishes chronology and log evidence, not branch-wide merge impact.
