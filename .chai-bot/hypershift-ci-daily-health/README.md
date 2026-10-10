# HyperShift CI Daily Health

An autonomous daily workstream that turns raw CI signal into a **prioritized, tracked, and
accountable** path to recovery — posted as one Slack thread each day, backed by a deterministic
companion and by Jira as the durable state store.

It is deliberately **not** a passive scoreboard. A scoreboard re-states "KubeVirt is 25% again
today" every morning until everyone tunes it out — it shows *status*, not *what to do*. This skill
instead surfaces what is **critical right now**, confirms it against the real failures, opens and
drives the tracking, and keeps owners accountable until the SLO is restored.

---

## Why it exists

The previous report was a per-platform pass-rate scoreboard. The team found it low-value: it
didn't prioritize (the one permafailing job that's blocking a release was buried inside a platform
average), it raised a broad "everything below 50%" incident that didn't match anyone's
situational awareness, and — most importantly — it had **no memory and no follow-through**. Nothing
got tracked, nudged, or driven to closure.

This skill is built around the opposite premise: **detection isn't the job — recovery is.** Every
day it answers "what is most broken, is it really broken, who owns fixing it, and is that work
actually moving?"

## What it does — the value

- **Surfaces the top priority, not everything.** Release blockers (permafailing payload /
  component-readiness periodics), merge-queue blockers and candidates, and below-SLO periodics —
  each as the **specific job**, never a platform average that hides it. All aggregation/scoreboards
  live in the **attached HTML** you can open from the channel; the thread stays a worklist.
- **Confirms before it claims.** It reads the **actual run logs** to triage merge-queue candidates
  (real break / flaky / false alarm / inconclusive), and resolves "is this release blocker *actually* holding up a
  live payload?" from the **release controller** (the blocking verification's real result on the
  newest payload tag — including the manual-**override** case: shipped despite a failed gate), not
  from a Prow streak.
- **Drives recovery and momentum.** A daily, **diff-only** Periodics-Health (SLO) workstream sorts
  below-SLO periodics into *newly breached / deteriorating / fix-or-retire / recovered* and reconciles
  a Jira lifecycle — **open → nudge → escalate → close** — verifying each surfaced breach
  against its current failure so the tracking stays faithful (and the linked defect still matches).
- **Creates accountability.** A **Team Action Items** worklist names what the *team* must do next;
  tracking sub-tasks are created **unassigned** and the bot **nudges** stale/ownerless ones. Jira is
  the durable state store — blocked-since and the fix-or-retire clock are read fresh from it each run.
- **Is safe by construction.** The bot **reconciles** Jira directly — search first, then create
  what's missing, update what drifted, and close what's resolved — and it **recommends** an incident
  only for a permafailing blocker. On any data gap it **fails closed** (reports `Unknown`, never green).
- **Is reproducible.** A deterministic, stdlib-only **Python companion** does all the data
  gathering, two-axis classification, and payload attribution and emits a bounded JSON document plus
  the HTML report; the LLM adds judgment and composes the Slack messages. The deterministic parts are
  unit-tested, so verdicts don't drift.

## The daily thread — how to read it

One thread per day. **Slack is the worklist; the attached HTML is the scoreboard** (open it for the
full per-job/per-platform picture). One Slack message per phase:

1. **Phase 1 — Parent report.** A read-only situational report: overall + trend; **Release
   Blockers** (each with *Blocking payload(s): Yes/No*, the blocked payload linked or flagged
   *overridden*, and the last accepted payload per stream); and **Merge-Queue Blockers** (*Confirmed*
   vs *Candidate*). Candidates are *listed*, never judged here — and there is no incident, no action
   items, and no attached report yet.
2. **Phase 2 — Candidate triage.** Opens the real Prow run logs and verdicts each merge-queue
   candidate — **real break / flaky / false alarm / inconclusive**. The Jira that follows is written in Phase 4.
3. **Phase 3 — Periodics Health (SLO).** The diff-only recovery workstream above — surfaces the
   lifecycle actions for below-SLO periodics (excluding the ones already flagged as release blockers).
   The Jira that follows is written in Phase 4.
4. **Phase 4 — Jira reconciliation (all Jira work).** The single place that **reconciles** every
   Jira change — release blockers, triaged breaks, flaky tests, and the periodics-health lifecycle —
   search first, then create / update / close, reported as a nested tree with links.
5. **Phase 5 — Incident, action items & report.** The final, consolidated message, posted
   independently of Phase 4: the **recommended incident**, the collective **Team Action Items**,
   and the **attached HTML report** — the collection snapshot with chaibot's end-of-run narrative
   annotations on top.

## How it decides — the model

- **Two independent axes.** *Axis A — permafailing*: a required presubmit, or a payload-blocking /
  component-readiness periodic, that is **stuck failing** (enough failures since its last pass, over
  ≥ 2 days). This drives blockers and the incident. *Axis B — SLO + trend*: pass-rate vs the SLO
  plus a confidence-aware week-over-week trend. **"Healthy" means exactly one thing: meeting its
  SLO.** **Flaky** is tracked per *test*, never as a job class.
- **The incident is permafail-based, not rate-based.** A "everything below 50%" net floods the
  recommendation and stops matching the team's awareness; a single umbrella incident is recommended
  only for **permafailing** blockers (already ≥ 2 days red by construction).
- **Payload-blocking is a fact, not a guess.** A release blocker is "currently blocking a payload"
  only when its blocking verification on the stream's newest tag is not `Succeeded` — resolved from
  the release controller, with the **override** case detected (tag `Accepted` yet the gate `Failed`).

## Jira model — the tracking spine

`CNTRLPLANE` is the spine (what *we* manage); `OCPBUGS` are linked **defect** records.

```
OCPSTRAT Feature (per release)          ← strategic anchor, found by the hcp-ci-release-tracker label
└── CNTRLPLANE Epic (per dev cycle)     ← "HyperShift CI Daily Health — <release>"
    ├── Story: release-blocker (per release)
    ├── Story: merge-queue (per branch)
    ├── Story: periodics-health / SLO (per release)
    └── Story: flake (per dev cycle)
         └── Sub-task (per job / per test)  ← the job's health, SLO, and recovery live here
              └──link── OCPBUGS (per defect) ← one defect for life; never re-pointed; never
                                               carries a job's SLO/health state
```

**OCPBUGS invariant:** one OCPBUGS tracks exactly one defect for its whole life — you may update it
with findings and close it when the defect is gone/flaky/obsolete, but you never re-point it at a
different defect, and a job's health/SLO state lives **only** on its CNTRLPLANE sub-task.

## Data sources

- **Sippy** — **the health source of truth**: pass-rate, week-over-week trend, and the sparkline
  (from `/api/jobs/analysis`), the component-readiness gate set (`/api/jobs`), and per-test flake
  alerts (`/api/tests/recent_failures`). The companion computes the 1w window itself.
- **CI Health dashboard** (`/api/job-registry`) — the static job **inventory**: names, roles,
  required flags, release-controller participations, and prow URLs (used to pick the job set and the
  N-4 scope).
- **Prow** — ordered run history (permafail streaks; the logs read during triage).
- **Release controller** — payload **status** context: the newest tag's phase, the last accepted
  payload, and the per-job blocking verification results used for payload-blocking attribution and
  override detection. Never an Axis-A/B driver.

## Layout

```
.chai-bot/hypershift-ci-daily-health/
├── README.md                              # you are here
├── hypershift_ci_daily_health_report.md   # the chaibot prompt (skill body)
├── scripts/                               # the deterministic companion
│   ├── hypershift-ci-daily-health.py      # collect (data + classification) + report (HTML)
│   ├── config.toml                        # single source of truth for every knob
│   ├── test_*.py + testdata/              # offline tests + fixtures
├── references/                            # per-phase workflows + the long-form model docs
│   ├── phase-1-parent-report.md … phase-5-incident-and-report.md
│   ├── classification.md · data-sources.md · jira-model.md · diagnostic-hints.md · development.md
└── assets/                                # the HTML report template
```

## Running & verifying

```console
# offline tests (no network)
make verify-hypershift-ci-daily-health

# the companion (read-only; never writes Slack or Jira)
python3 scripts/hypershift-ci-daily-health.py collect --as-of <RFC3339> \
  --source-revision <sha> --data-out /tmp/data.json --html-out /tmp/report.html
python3 scripts/hypershift-ci-daily-health.py report --data /tmp/data.json --html-out /tmp/report.html
```

## Go deeper

- [The prompt](hypershift_ci_daily_health_report.md) — the skill body the bot follows.
- [classification.md](references/classification.md) — the two-axis decision tree and knobs.
- [jira-model.md](references/jira-model.md) — the OCPSTRAT → epic → story → sub-task → OCPBUGS model.
- [data-sources.md](references/data-sources.md) — the public sources and their authority.
- [development.md](references/development.md) — the companion (dev-facing) + roadmap.
- Per-phase workflow + message format + example: [phase-1](references/phase-1-parent-report.md) ·
  [phase-2](references/phase-2-candidate-triage.md) · [phase-3](references/phase-3-periodics-health.md) ·
  [phase-4](references/phase-4-jira-bookkeeping.md) · [phase-5](references/phase-5-incident-and-report.md).
