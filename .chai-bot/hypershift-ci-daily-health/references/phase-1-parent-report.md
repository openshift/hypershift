# Phase 1 — Parent report

You **MUST NOT** triage presubmit candidates, open run logs, assert failure causes, or classify
anything here. Candidates are **listed** (a deterministic classification), never judged — no
verdict, no count‑by‑verdict, no cause. You **MUST** defer all triage to Phase 2.

## Workflow

1. **Collect the deterministic data.** Check out `openshift/hypershift`; set once
   `SOURCE_REVISION = git rev-parse --verify 'HEAD^{commit}'` and `T` (RFC3339 UTC):
   ```text
   python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py collect \
     --as-of "$T" --source-revision "$SOURCE_REVISION" --data-out /tmp/hcih-data.json
   ```
   Read `scope`, `presubmits`, `presubmit_candidates`, `periodics`, `incident_set`,
   `flaky_tests`, `job_health_below_slo`. If `scope.state` is `unknown`, flag it as a **single
   concise line** (the gap count only) — MUST NOT guess past the gaps or dump the full list.
2. **Resolve each permafailing item against Jira.** For every `incident_set` entry and every
   `job_health_below_slo` job, search for the unresolved per‑branch/per‑release CNTRLPLANE story
   (under the current dev‑cycle epic) and its children. A permafailing blocker is **already ≥ 2
   days red** → incident‑eligible **now**; if no story exists, plan to **open** one — MUST NOT
   withhold the incident for a missing story. (See `jira-model.md`.)
3. **Write annotations** to `/tmp/hcih-annotations.json`:
   `{"summary": "...", "incident": "...", "job_notes": {"<job_id>": "..."}}` (all OPTIONAL).
4. **Render the report:**
   ```text
   python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py report \
     --data /tmp/hcih-data.json --annotations /tmp/hcih-annotations.json --html-out /tmp/hcih-report.html
   ```
5. **Compose and post** the parent message per the template below, and **attach
   `/tmp/hcih-report.html`** to it. Produce every section, in order; do not mix periodics and
   presubmits. The only candidate line allowed in Team Action Items is "Triage replies for the N
   candidates follow in‑thread"; any "Approve opening OCPBUGS/CNTRLPLANE" item covers **only**
   the permafailing blockers.

## Message format

Styling follows the main prompt's Slack rules (mrkdwn styling + Markdown links, an emoji on
every section header, nested `*` sub-bullets with one fact per line). Nested sub-labels (a
release, `Confirmed` / `Candidate`) use **bold + indentation**, no emoji. Legend:

| Header | Shortcode |
|---|---|
| Title — HyperShift CI Daily Health | `:mega:` |
| Overall + Trend | `:bulb:` |
| Release Blockers (blocking periodics) | `:openshift:` |
| Merge Queue Blockers (required PR checks) | `:pr-open:` |
| Incident | `:rotating_light:` |
| Team Action Items | `:done-circle-check:` |

Inline status shortcodes — Overall + Trend only, trailing the text: `:red_circle:` blockers ·
`:green-up-arrow:` improving · `:down-arrow-red:` degrading · `:check:` stable. The
Unknown-scope line uses `:warning:`. Release Blockers group **per release**; Merge Queue
Blockers split into `Confirmed` (permafailing) and `Candidate` (listed for Phase 2), each
grouped by branch with plain-text job names — **no verdicts on candidates**.

The *Team Action Items* are the **human / RITS worklist** — what the *team* must do next
(declare the incident, assign owners, approve opening issues). They are **not your (the bot's)
tasks**; you only compile and post them.

### Template

```
:mega: HyperShift CI Daily Health — <DD Mon, HH:MM UTC>
HTML report attached.

*:bulb: Overall + Trend*
* <n> release-blockers across <releases>. :red_circle:
* Trend
  * <n> improving :green-up-arrow: / <n> degrading :down-arrow-red: / <n> stable :check:.
* SLO
  * <n> jobs below the <SLO>% SLO.

*:openshift: Release Blockers (blocking periodics)*
* *OCP <release>*
  * <job> — <streak> fails, <rate>%, payload <phase>.

*:pr-open: Merge Queue Blockers (required PR checks)*
* Confirmed
  * <branch> — <job> (<streak> reds)
* Candidate (triage in thread)
  * <branch> (<n>) — <job>, <job>, <job>

*:rotating_light: Incident*
* <plain-language line: which releases/branches are blocked, and why>.
* No tracking stories yet; I will open the per-release release-blocker stories on approval.

*:done-circle-check: Team Action Items*
* <human worklist item>

:warning: _Scope: Unknown — <n> coverage gaps; see attached report._
```

A permafailing blocker is already ≥ 2 days red by construction, so it is incident-eligible on
its own — MUST NOT wait for a Jira story to exist before proposing; if none exists, say you will
open one.

### Filled example

```
:mega: HyperShift CI Daily Health — 30 Sep, 12:00 UTC
HTML report attached.

*:bulb: Overall + Trend*
* 2 release-blockers across 5.1, 5.0, 4.22, 4.21, 4.20. :red_circle:
* Trend
  * 1 improving :green-up-arrow: / 8 degrading :down-arrow-red: / 31 stable :check:.
* SLO
  * 43 jobs below the 80% SLO.

*:openshift: Release Blockers (blocking periodics)*
* *OCP 4.20*
  * e2e-aks — 42 fails, 0%, payload Ready.
* *OCP 4.22*
  * e2e-v2-aws — 26 fails, 0%, component-readiness gate, payload Unknown.

*:pr-open: Merge Queue Blockers (required PR checks)*
* Confirmed
  * none
* Candidate (triage in thread)
  * main (7) — e2e-aks, e2e-aws, e2e-aws-5-0, e2e-aws-upgrade-hypershift-operator, e2e-kubevirt-aws-ovn-reduced, e2e-v2-aws, e2e-v2-azure-self-managed
  * release-4.20 (1) — e2e-kubevirt-aws-ovn-reduced

*:rotating_light: Incident*
* 4.20 and 4.22 release payloads are blocked — each has a blocking periodic permafailing > 2 days.
* No tracking stories yet; I will open the per-release release-blocker stories on approval.

*:done-circle-check: Team Action Items*
* Declare and handle the proposed incident.
* Approve opening OCPBUGS + CNTRLPLANE for e2e-aks (4.20) and e2e-v2-aws (4.22).
* Triage replies for the 8 candidates follow in-thread.

:warning: _Scope: Unknown — 34 coverage gaps; see attached report._
```
