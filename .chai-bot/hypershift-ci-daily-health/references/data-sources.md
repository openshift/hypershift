# Public data sources and their authority

The companion reads these; you (the bot) MUST only follow the canonical public URLs it
emits. All source URLs live in `config.toml` under `[sources]`. **Sippy is the health source
of truth** — the companion queries it directly and computes the 1w health window itself. The
**dashboard** is read only for the static **job inventory**; the **release controller** only for
the live payload phase; **Prow** only for ordered presubmit runs.

## CI Health dashboard — job inventory (`/api/job-registry`)

`GET /api/job-registry` (`job-registry/v7`) returns the full job inventory as JSON
(`{api_version, jobs[]}`). Each job carries `id`/`name`, `type` (`presubmit`/`periodic`),
top-level `e2e_framework` (`v1`/`v2`/`none`), `versions`, `platforms`, `prow_job_history_url`,
and — for presubmits — `presubmit.{target_branch, target_release, required,
sippy_ingestion.enabled}`, and — for periodics — `release_controller[]` participations, each
with `stream.{name, release, kind, architecture, end_of_life, release_status_url}` and
`verification.{name, role}` (`blocking`/`informing`/`async`/`disabled`).

The companion selects the job set it needs — required presubmits (with an e2e framework, on a
supported branch) plus **blocking** payload periodics — and restricts it up front to the in-scope
release window, then asks Sippy for each selected job's health. The dashboard only supplies this
inventory.

**Scope.** The development release N is the max `target_release` across the registry's
**main-branch** presubmits (overridable via `[scope].development_release`). Supported scope =
N plus `[scope].supported_predecessors` predecessors (N-4 ⇒ 4), computed deterministically by
`releaseRank` (`4.x → x`; `5.x → 23 + x`), minus `[scope].excluded_releases` (`4.23`, which
shares a rank with `5.0`). You MUST NOT hardcode a release list.

## Sippy — health source of truth

`sippy.dptools.openshift.org`, three endpoints:

- **`GET /api/jobs/analysis`** — per selected job (presubmits under release `Presubmits`;
  periodics under their release), `period=hour` over the window the 1w view needs. Returns
  `by_period` (hourly `{total_runs, result_count}`), from which the companion computes the
  two-axis inputs: **rate** (= `S / total_runs`), **prev** (prior 7 days), **week-over-week
  trend**, and the 28×6h **sparkline** (`[runs, passes(S), test_fails(F), infra_fails(n+N)]`
  per slot). Result codes other than `S/F/n/N` count only toward `total_runs`.
- **`GET /api/jobs`** — per in-scope release, the periodics whose `variants` include
  `JobTier:standard` — the **component-readiness** gate set (a second release gate; a
  permafailing member is a release blocker too).
- **`GET /api/tests/recent_failures`** (Presubmits, 4h vs 24h) — per-test failure signals
  (`test_name`, `outputs[].prow_job_name`) → the flaky-**test** alerts (kept only when they
  touch a selected presubmit).

**Rate.** `rate = passes / total_runs` — infra and aborted runs are counted in the
denominator (one number, so a job dragged below the SLO by infra stays visible). The permafail
pattern treats infra as a retest tax, but the SLO uses `rate`.

**Nullability / fail-safe.** A per-job analysis that **fails** (request error) is absent →
that job reports `rate`/`sparkline` `null` ⇒ **Unknown**, never green. A present-but-empty
analysis (the request succeeds with no runs) yields a real `0.0` rate. A non-ingested
(release-branch) presubmit is not a Sippy analysis target and reports no window data (disambiguated
via `sippy_ingestion_enabled` / `runs > 0`).

## Release controller — payload status (context only)

`amd64.ocp.releases.ci.openshift.org` (and the `multi.` host) are read **only** for payload
status, per stream a blocking periodic advertises in the registry's `participations[]`. Two
reads per gated stream:
- `GET /api/v1/releasestream/{stream}/tags` — tags newest-first, each with a `phase`
  (`Pending`/`Ready`/`Accepted`/`Rejected`); gives the newest tag's phase and the newest
  **Accepted** tag (the last-good payload).
- `GET /api/v1/releasestream/{stream}/release/{tag}` — the newest tag's `results.blockingJobs`:
  each gate's `state` keyed by its `verification_name`.

These answer deterministically whether a release-blocking periodic is **currently** holding up a
live payload (its blocking verification on the newest tag is not `Succeeded`) and which payload —
including the **override** case (tag `Accepted` yet the gate `Failed`). Payload status is **context
only**; it is never the permafail/incident driver — that comes from the deterministic Axis-A class.

## Prow — ordered run logs

Public Prow job-history (`prow_job_history_url`, from the registry) provides ordered runs,
states, timestamps, PR head SHAs, and run links. The companion uses these for the run-level
permafail streak (presubmits always; periodics when exact run order is needed); you use the
canonical run links it emits to read logs when triaging a candidate. Prow establishes
chronology and log evidence, not branch-wide merge impact. When a message or the HTML surfaces
an exploration link, prefer the Sippy job/component-readiness views for aggregate health.
