# Reading the daily CI health report (RITS / IC guide)

The chaibot daily CI health report is a single automated Slack thread each morning. It is
not an IC ping — but the incident it *proposes* is meant to be acted on. This guide
explains what you're looking at. See the RITS/IC role doc (`rits-ic.md`) for the
surrounding process.

## The parent message (at a glance)

chaibot composes it from the companion's deterministic data plus its Jira lookups:

- **Overall + Trend** — the headline state and week-over-week movement.
- **Release Blockers (blocking periodics)** — per supported release (N-4), which blocking
  periodics (payload-blocking or component-readiness) are permafailing, aggregated per release,
  with payload phase as context.
- **Merge Queue Blockers (required PR checks)** — per supported branch, split into *Confirmed*
  (required presubmits that are permafailing) and *Candidate* (flagged jobs triaged in-thread).
- **Proposed Incident** — the single incident chaibot proposes: branches/releases
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

First reply is *Merge Queue Candidates Triage* — the candidates grouped by branch, each job led
by a verdict emoji (🔴 real break · 🌊 flaky · ⚪ false alarm) with its evidence in sub-bullets
(signature · scale · Jira action · run link). **Full evidence lives in the Jira issue**, not the
channel. chaibot proposes the Jira changes; after your approval it opens/links the CNTRLPLANE
sub-tasks + OCPBUGS defects and posts a *Jira Updated* reply with an updated Action Items list.

## Jira

Four `rits-work` stories on the Hosted Control Plane component (per-release
release-blocker, per-branch merge-queue, per-release flake, per-release job-health). Each
confirmed blocker/flake has an OCPBUGS defect bug and a CNTRLPLANE subtask (the next
action), created unassigned for RITS to pick up. Jira is the durable state store — blocked
durations and the fix-or-retire clock are read from it. Tuning thresholds is a one-file PR
to `config.toml`.
