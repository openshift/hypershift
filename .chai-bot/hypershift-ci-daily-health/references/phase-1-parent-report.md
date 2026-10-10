# Phase 1 — Parent report

## Overview

Phase 1 is a **read-only situational report** of the companion's deterministic verdicts: the overview
scoreboard, the release blockers, and the merge-queue blockers + candidates (candidates are *listed*,
never judged). It is Phase 1 of the five-phase pipeline (see *Pipeline & run directory* and *The data
document* in the main prompt). It opens the run: it creates the run directory and collects `data.json`.
There is **no** triage, **no** Team Action Items, **no** incident, **no** Jira, and **no** attached
report here — those belong to Phases 2–5.

## Inputs

**Constraints:**
- You run first; there is no prior handoff to read. You MUST create `data.json` yourself (Step 1).
- Read the report's facts only from `data.json`: `at_a_glance`, `incident_set.release_blockers[]`
  (enriched), `incident_set.merge_queue_blockers[]`, `presubmit_candidates[<branch>][]`, and `scope`.
- You MUST NOT recompute, re-tally, or hand-join any of these **because** the companion already emitted
  them and a hand-computed value would disagree with the Phase-5 HTML report.
- You MUST NOT read Jira in Phase 1 **because** Phase 1 is a pure render of the companion's output.

## Steps

### 1. Set up the run directory and collect
Create a clean run directory for today and collect the data document into it using the companion script.

**Constraints:**
- You are already inside the `openshift/hypershift` worktree. You MUST set `T` to the run's as-of
  (`T="$(date -u +%Y-%m-%dT%H:%M:%SZ)"`, RFC3339 UTC) and `SOURCE_REVISION="$(git rev-parse --verify
  'HEAD^{commit}')"`. You MUST NOT clone or switch branches **because** that would change the checked-out
  revision the collect is reported against.
- You MUST derive the run directory from `T`'s UTC day and reset it, then collect:
  ```text
  RUN_DIR="/tmp/hypershift-ci-daily-health/$(date -u -d "$T" +%F)"
  rm -rf "$RUN_DIR" && mkdir -p "$RUN_DIR"
  python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py collect \
    --as-of "$T" --source-revision "$SOURCE_REVISION" --data-out "$RUN_DIR/data.json"
  ```
- You MUST seed `$RUN_DIR/journal.md` with a header recording the run date, `$T`, `$SOURCE_REVISION`,
  and `$RUN_DIR`.
- **Gate:** you MUST confirm `collect` exited 0 and `data.json` parses before Step 2. If it did not, go
  to Step 5 (fail closed).

### 2. Read the deterministic verdicts and author the overview
Read the emitted facts; author the `overview` body from `at_a_glance`.

**Constraints:**
- You MUST build the `overview` body only from `data.json.at_a_glance` and `scope.releases`: the scope
  list, `release_blockers` count, the `trend` split (`improving`/`degrading`/`stable`, plus
  `insufficient` when non-zero), and `jobs_below_slo` against the SLO. You MUST NOT count these yourself
  **because** `at_a_glance` is the authoritative, single-sourced tally.
- Status emoji are **rule-bound, not decorative**. You MUST append `:red_circle:` to the
  `*Release blockers:*` line when, and only when, `release_blockers > 0`, and you MUST NOT add a status
  emoji to any other overview line **because** otherwise the verbatim-posted message drifts
  emoji-by-emoji run to run.
- You MUST NOT state the SLO as a literal (e.g. "80%") — cite it as "the SLO" (its value is
  `[job_health].slo_pass_rate_percent`) **because** a hardcoded number goes stale when the knob changes.

### 3. Render the Release Blockers from the enriched record
Render each `release_blockers[]` entry straight from its fields — no lookup into `periodics[]`.

**Constraints:**
- You MUST render each entry using only the fields **on the entry**, in this order:
  1. **Blocking payloads**, from `blocking_state`:
     - when `yes`, list each `blocked_payloads[]` tag as a link, and prefix the line
       `:warning: Yes — … overridden` when the entry's `overridden` is true;
     - when `no`, render `No`, and append `— component-readiness gate` when `gate` is
       `component-readiness` or `both`;
     - when `unknown`, render `Unknown`.
  2. **Scale** — `<streak_runs> failures; <rate>% pass rate`, rounding `rate` (a 0–100 percent) to the
     nearest whole number.
  3. **Last accepted release payload** — each `last_accepted[]` tag, linked.
- You MUST group entries by `release` under one `• *OCP <release>*` header, each blocker a `◦` beneath it,
  and you MUST NOT repeat the header.
- You MUST NOT read any field that is not on the `release_blockers[]` entry, and you MUST NOT treat
  `blocking_state` and `gate` as the same thing **because** they are orthogonal axes (a payload-gated job
  can be `blocking_state: no`).

### 4. Render the Merge-Queue section (Confirmed vs Candidate)
Split into confirmed permafailing blockers and the candidates Phase 2 will triage.

**Constraints:**
- You MUST render `*Confirmed*` as one `◦ *<branch>*` per branch in `merge_queue_blockers[]`, then one
  `:black_small_square:` line per blocker's `` `name` `` (code). When `merge_queue_blockers` is empty, the
  body MUST be exactly `◦ None found in assessed data.`
- You MUST render `*Candidate — triage to follow*` as the candidates per branch, with **no verdicts, no
  counts, no causes**. When there are no candidates in any branch, the body MUST be exactly `◦ None found
  in assessed data.` You MUST NOT triage, open run logs, or assert a cause **because** that is Phase 2's job.

### 5. Fail-closed render (when collection is incomplete)
Handle a failed collect or a document whose `scope.state` is `unknown`.

**Constraints:**
- If Step 1's gate failed, you MUST keep `title_time` as the run timestamp and post every **other**
  section body as `:warning: Unknown — data collection failed: <error>`, then stop.
- If `scope.state` is `unknown`, the `partial_data` body MUST be a single line naming the gap count
  (the number of `source_failures` plus `coverage_uncertainties`); you MUST NOT guess past the gaps **because**
  fail-closed forbids inventing data. When `scope.state` is `complete`, you MUST leave the `partial_data` body empty.

## Output: Slack message

Author only the **section bodies** into `$RUN_DIR/phase-1-bodies.txt` (plain text, sections separated
by `@@ <key>` marker lines — no escaping), then render and post:

```text
python3 .chai-bot/hypershift-ci-daily-health/scripts/hypershift-ci-daily-health.py render-message \
  --phase 1 --bodies "$RUN_DIR/phase-1-bodies.txt" --out "$RUN_DIR/phase-1-message.txt"
```

Post `$RUN_DIR/phase-1-message.txt` **verbatim**. The template owns the `:mega:` title, the section
headers, the divider bars, and the `(part 1/5)` footer — never type those.

**Sections** (one `@@ <key>` each, all required): `title_time` (`<DD Mon, HH:MM UTC>`), `overview`,
`release_blockers`, `merge_queue`, `partial_data` (empty body when `scope.state` is `complete`).

**Body styling** (main-prompt rules): bullets `• ` / `  ◦ ` (two spaces) / `    :black_small_square: `
(four spaces); **bold** the leading label (`*Scope:*`, `*OCP <release>*`, `*Confirmed*`); **code** each
`` `job name` ``; **link** each payload tag `[tag](url)`. One fact per line, one code span per line —
**except** the candidate list, which MAY enumerate a branch's candidate `` `name` ``s on one `◦` line.

### Example `phase-1-bodies.txt`
This is the real file. The `<--` notes explain the Slack formatting mechanic — **do not put them in the
actual file.**

```
@@ title_time
30 Sep, 12:00 UTC
@@ overview
• *Scope:* OCP 5.1, 5.0, 4.22, 4.21, 4.20.            <-- bold via * *
• *Release blockers:* 2. :red_circle:
• *Trend:*
  ◦ 1 improving / 8 degrading / 31 stable.            <-- level 2: two spaces, then ◦
• *SLO:*
  ◦ 43 jobs below the SLO.
@@ release_blockers
• *OCP 4.20*                                           <-- bold via * *
  ◦ `e2e-aks`                                          <-- job name in code backticks
    :black_small_square: Blocking payloads: Yes — [4.20.0-0.ci-2026-09-28-030312](https://amd64.ocp.releases.ci.openshift.org/releasestream/4.20.0-0.ci/release/4.20.0-0.ci-2026-09-28-030312).   <-- link via [text](url)
    :black_small_square: 42 failures; 0% pass rate.    <-- level 3: four spaces, then :black_small_square:
    :black_small_square: Last accepted release payload: [4.20.0-0.ci-2026-09-10-120000](https://amd64.ocp.releases.ci.openshift.org/releasestream/4.20.0-0.ci/release/4.20.0-0.ci-2026-09-10-120000).
• *OCP 4.22*
  ◦ `e2e-v2-aws`
    :black_small_square: Blocking payloads: No — component-readiness gate.
    :black_small_square: 26 failures; 0% pass rate.
@@ merge_queue
• *Confirmed*
  ◦ None found in assessed data.
• *Candidate — triage to follow*
  ◦ *main*: `e2e-aks`, `e2e-aws`, `e2e-v2-aws`.        <-- the one allowed multi-code line
  ◦ *release-4.20*: `e2e-kubevirt-aws-ovn-reduced`.
@@ partial_data
:warning: Partial data: 34 coverage gaps could not be fully assessed this run (Unknown).
```

## Output: handoff + journal

**Constraints:**
- Append a dated entry to `$RUN_DIR/journal.md` (what you posted; scope/partial state).
- Write `$RUN_DIR/phase-1-handoff.md` with the facts the later phases need, in these sections:
  - `## scope` — the releases, and the partial-data state (complete, or the gap count).
  - `## release_blockers` — per release: each blocker's `job_id`, `name`, `gate`, `blocking_state`,
    `rate`, `streak_runs`, and blocked-payload tags. (Phase 4 reconciles these.)
  - `## merge_queue_confirmed` — per branch: each permafailing blocker's `job_id`, `name`.
  - `## merge_queue_candidates` — per branch: each candidate's `job_id`, `name`, and its run links from
    `presubmit_candidates[<branch>][].runs[].url`. (Phase 2 triages these.)

## Definition of done
`phase-1-message.txt` posted verbatim; `phase-1-handoff.md` written in the schema above; the
`journal.md` entry appended. No triage, no incident, no Jira, no report, no look-ahead.
