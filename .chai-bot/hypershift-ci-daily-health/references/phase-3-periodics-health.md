# Phase 3 — Periodics Health (SLO)

The work of this phase is **driving SLO restoration for periodics** — the release-payload
periodics below the SLO that are **not** already flagged as permafailing release blockers (those
are Phase 1's job). The Slack reply is **diff-only**: it surfaces just what *changed* since the
tracked state and proposes the Jira lifecycle actions. Skip if there is nothing to surface.

**Scope:** every **periodic** in `job_health_below_slo` (`kind == periodic`) **except** those in
the Phase-1 flagged blocker set (the permafailing release blockers — they're tracked there).
Presubmit SLO health is out of scope for this phase.

## Workflow

1. **Build today's below-SLO periodics set** from `job_health_below_slo`: keep entries with
   `kind == periodic` and `flagged_blocker == false`. The companion pre-computes both fields
   (`flagged_blocker` = already a permafailing release/merge-queue blocker, tracked elsewhere),
   so you filter **directly** — no lookup or join.
2. **Diff it against the open periodics-health sub-tasks in Jira** (Jira is the living artifact —
   there is no yesterday's data doc). Sort each periodic into exactly one bucket:
   - 🆕 **Newly breached** — below SLO, no open sub-task → propose **opening** a restore
     sub-task (link the OCPBUGS cause when known).
   - 📉 **Deteriorating** — has a sub-task, still below SLO, and the entry's `trend` is
     `degrading` → **nudge** (flag no owner / no progress).
   - ⏳ **Fix-or-retire** — sub-task aged past `[job_health].fix_or_retire_horizon_days` with no
     progress → surface the **fix-or-retire decision** (human decides).
   - ✅ **Recovered** — has a sub-task, back above SLO → propose **closing** it.
   A periodic still below SLO whose sub-task is healthy (owner + progress, not deteriorating) is
   *steady* — **do not surface it** (diff-only).
3. **Post one reply** grouped by bucket per the template below, ending with the approval line.
   You only **propose**; the approved lifecycle changes are executed in Phase 4.

## Message format

Styling per the main prompt's Slack rules (mrkdwn + Markdown links, an emoji on every section
header, nested `*` sub-bullets — one fact per line). Under each bucket, a **bold release
sub-label**, then the periodic with its evidence in sub-bullets (rate · trend/age · action ·
tracking link). Legend:

| Header | Shortcode |
|---|---|
| Periodics Health — SLO | `:thermometer:` |
| ↳ Newly breached | `:new:` |
| ↳ Deteriorating | `:down-arrow-red:` |
| ↳ Fix-or-retire | `:hourglass:` |
| ↳ Recovered | `:green-up-arrow:` |

### Template

```
:thermometer: *Periodics Health — SLO*

*:new: Newly breached*
* *OCP <release>*
  * <periodic> — *<rate>%*
    * ↓ from <prev>% WoW
    * → open restore sub-task (link cause when known)

*:down-arrow-red: Deteriorating*
* *OCP <release>*
  * <periodic> — *<rate>%*
    * ↓ WoW · breached <n>d · no owner
    * → nudge [[CNTRLPLANE-…] restore <periodic>](https://issues.redhat.com/browse/CNTRLPLANE-…)

*:hourglass: Fix-or-retire (past <horizon>d)*
* *OCP <release>*
  * <periodic> — *<rate>%*
    * breached <n>d · no progress
    * → decide fix or retire [[CNTRLPLANE-…]](https://issues.redhat.com/browse/CNTRLPLANE-…)

*:green-up-arrow: Recovered*
* *OCP <release>*
  * <periodic> — *<rate>%*
    * back above the <SLO>% SLO
    * → close [[CNTRLPLANE-…]](https://issues.redhat.com/browse/CNTRLPLANE-…)

_Reply `approve` to open / nudge / escalate / close the above; I'll confirm in the Jira update._
```

### Filled example

```
:thermometer: *Periodics Health — SLO*

*:new: Newly breached*
* *OCP 4.21*
  * e2e-azure — *68%*
    * ↓ from 82% WoW
    * → open restore sub-task (link cause when known)

*:down-arrow-red: Deteriorating*
* *OCP 5.0*
  * e2e-aws-ovn — *74%*
    * ↓ WoW · breached 9d · no owner
    * → nudge [[CNTRLPLANE-12040] restore e2e-aws-ovn](https://issues.redhat.com/browse/CNTRLPLANE-12040)

*:hourglass: Fix-or-retire (past 21d)*
* *OCP 4.21*
  * e2e-metal — *55%*
    * breached 24d · no progress
    * → decide fix or retire [[CNTRLPLANE-12010] restore e2e-metal](https://issues.redhat.com/browse/CNTRLPLANE-12010)

*:green-up-arrow: Recovered*
* *OCP 5.1*
  * e2e-aws-conformance — *91%*
    * back above the 80% SLO
    * → close [[CNTRLPLANE-11990] restore e2e-aws-conformance](https://issues.redhat.com/browse/CNTRLPLANE-11990)

_Reply `approve` to open / nudge / escalate / close the above; I'll confirm in the Jira update._
```
