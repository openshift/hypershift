# Reading the daily CI health report (RITS / IC guide)

The chaibot daily CI health report is a single automated Slack thread each morning. It is
not an IC ping — but the incident it *proposes* is meant to be acted on. This guide
explains what you're looking at. See the RITS/IC role doc (`rits-ic.md`) for the
surrounding process.

## The parent message (at a glance)

chaibot composes it from the companion's deterministic data plus its Jira lookups:

- **Overall + Trend** — the headline state and week-over-week movement.
- **Periodics — release payloads** — per supported release (N-4), which blocking periodics
  (payload-blocking or component-readiness) are permafailing, with payload phase as context.
- **Presubmits — merge queue** — per supported branch, which required presubmits are
  permafailing (confirmed blockers) and how many candidates are under investigation.
- **Proposed Incident** — the single jobs-incident chaibot proposes: branches/releases
  blocked > 2 days. **You** declare it, open the bridge, and post situational awareness —
  chaibot never does.
- **Action Items** — your worklist: handle the incident, ensure active owners for ≤ 2-day
  blockers, and any stale-tracker nudges.
- An **HTML trend report** (per-periodic pass-rate charts) is **attached as a file** so you
  can see the shape without opening the thread.

Periodics and presubmits are never mixed; each line is labelled release-blocker or
merge-queue-blocker.

## The two axes, in plain terms

Every job is judged on two independent questions:

- **Permafailing?** — is it *stuck failing*? A required presubmit, or a payload-blocking /
  component-readiness periodic, that is stuck (enough consecutive failures since its last
  pass, over enough days) is a blocker and can drive the incident. An inconclusive recent
  burst on a presubmit is a *candidate* chaibot investigates.
- **Meeting its SLO? (+ trend)** — is its pass rate above the threshold, and which way is
  it moving week-over-week? Below SLO = unhealthy → tracked (fix or retire), even if it
  isn't blocking. "Healthy" means exactly this: meeting its SLO.

**Flaky** is tracked per *test* (a specific test that fails intermittently), not as a job
label.

## The thread

Concise: one line per actionable item, linking out to its Jira issue. **Full evidence lives
in the Jira issue**, not the channel. chaibot proposes Jira changes and, after your
approval, creates/links the bugs and subtasks and confirms with links.

## Jira

Four `rits-work` stories on the Hosted Control Plane component (per-release
release-blocker, per-branch merge-queue, per-release flake, per-release job-health). Each
confirmed blocker/flake has an OCPBUGS defect bug and a CNTRLPLANE subtask (the next
action), created unassigned for RITS to pick up. Jira is the durable state store — blocked
durations and the fix-or-retire clock are read from it. Tuning thresholds is a one-file PR
to `config.toml`.
