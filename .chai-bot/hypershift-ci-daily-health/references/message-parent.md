# Slack parent message — the daily CI health report

This is the first message you post each run, and it **MUST be the report itself** — never a
"trends collected" placeholder. You MUST compose it from the `collect` data document plus
your Jira findings, MUST **attach the HTML file** (`/tmp/hcih-report.html`, the `report`
output) to this same message, and MUST keep its timestamp as the thread parent. It MUST be a
decision summary, not an evidence dump.

This is a **minimal-judgment** message: report the companion's deterministic verdicts and the
incident proposal for permafailing blockers only. You MUST NOT triage presubmit candidates or
assert failure causes here. You MAY **list** the candidate jobs (the flag is a deterministic
classification), but you MUST NOT attach any verdict, count-by-verdict, or cause: you MUST NOT
write "the 2 real-break candidates" or call any candidate real break / flaky / false alarm.
You MUST defer all candidate triage to the thread (Phase 2).

## Slack syntax

You MUST use **Slack mrkdwn** for styling — `*bold*`, `_italic_` — but you MUST write **links
as Markdown**: `[label](url)`. chaibot's Slack renders Markdown links; the mrkdwn `<url|label>`
form does **not** work here. You MUST NOT use `#` headings or `**double asterisks**`.

Bullets are **Markdown lists**: `* ` at the top level and `  * ` (two-space indent) for a
nested sub-bullet. Use nesting for the natural parent→child structure (release → its
periodics; Confirmed/Candidate → their branches). Keep job names in **plain text** — no
`` `code` `` boxes in this message.

**Every section header MUST begin with an emoji** (a Slack emoji shortcode), and headers are
bold: `*:shortcode: Header*`. Nested sub-labels (a release like `*OCP 4.20*`, or `Confirmed` /
`Candidate`) use **bold + indentation**, not their own emoji. Fixed legend:

| Header | Shortcode |
|---|---|
| Title — HyperShift CI Daily Health | `:mega:` |
| Overall + Trend | `:bulb:` |
| Release Blockers (blocking periodics) | `:openshift:` |
| Merge Queue Blockers (required PR checks) | `:pr-open:` |
| Incident | `:rotating_light:` |
| Action Items | `:done-circle-check:` |

Inline status shortcodes — used **only** in Overall + Trend, trailing the text: `:red_circle:`
(blockers) · `:green-up-arrow:` (improving) · `:down-arrow-red:` (degrading) · `:check:`
(stable). The Unknown-scope line uses `:warning:`.

**Post content only — never these instructions' scaffolding.** You MUST NOT copy guidance
labels (e.g. "Routing", "(once)"), internal jargon (e.g. "jobs-incident", "umbrella",
"Axis A/B"), or self-directed parentheticals into a message. Only what a template fence shows
gets posted; everything outside a fence is for you, not the channel.

## What to post

Produce **every** section below, in order. Periodics and presubmits MUST NOT be mixed. If
everything is clear, still post the parent with a one-line green summary.

- **Title** (`:mega:`) — the plain line `HTML report attached.` directly under it.
- **Overall + Trend** (`:bulb:`) — three top-level bullets: the release-blocker count (trailing
  `:red_circle:`), a `Trend` bullet with the improving/degrading/stable counts nested under it
  (each with its arrow / `:check:`), and an `SLO` bullet with the below-SLO count nested under it.
- **Release Blockers (blocking periodics)** (`:openshift:`) — the **permafailing** blocking
  periodics (payload-blocking or component-readiness), **one bold release bullet per release**
  (`* *OCP <release>*`) with its blocking periodic(s) nested beneath (rate, gate, payload
  phase). If none: a single `* none permafailing` bullet.
- **Merge Queue Blockers (required PR checks)** (`:pr-open:`) — two top-level bullets:
  `Confirmed` (permafailing required presubmits, branches nested) and `Candidate (triage in
  thread)` (flagged jobs, branches nested as `<branch> (<n>) — <plain job names>`). Empty ⇒ a
  nested `none`. No verdicts on candidates.
- **Incident** (`:rotating_light:`) — plain-language bullets: which releases/branches are
  blocked and why, then the tracking-story plan. You only propose (declaring, the bridge, and
  situational awareness are human). If none: `* none — no permafailing blocker`.
- **Action Items** (`:done-circle-check:`) — the human worklist. The only candidate line
  allowed is "Triage replies for the N candidates follow in-thread"; any "Approve opening
  OCPBUGS/CNTRLPLANE" item MUST cover **only** the permafailing blockers.
- If `scope.state` is Unknown, end with a **single** `:warning:` italic line stating the gap
  count only — never the full gap list (that lives in the attached report).

## Template

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
* *OCP <release>*
  * <job> — <streak> fails, <rate>%, <gate>, payload <phase>.

*:pr-open: Merge Queue Blockers (required PR checks)*
* Confirmed
  * <branch> — <job> (<streak> reds)
* Candidate (triage in thread)
  * <branch> (<n>) — <job>, <job>, <job>

*:rotating_light: Incident*
* <plain-language line: which releases/branches are blocked, and why>.
* No tracking stories yet; I will open the per-release release-blocker stories on approval.

*:done-circle-check: Action Items*
* <human worklist item>

:warning: _Scope: Unknown — <n> coverage gaps; see attached report._
```

A **permafailing blocker is already ≥ 2 days red** (that is the permafail criterion), so it is
incident-eligible on its own — you MUST NOT wait for a Jira story to exist before proposing. If
no tracking story exists yet, you MUST say you will open one.

## Filled example

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

*:done-circle-check: Action Items*
* Declare and handle the proposed incident.
* Approve opening OCPBUGS + CNTRLPLANE for e2e-aks (4.20) and e2e-v2-aws (4.22).
* Triage replies for the 8 candidates follow in-thread.

:warning: _Scope: Unknown — 34 coverage gaps; see attached report._
```
