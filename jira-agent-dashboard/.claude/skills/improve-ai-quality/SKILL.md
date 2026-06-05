---
name: improve-ai-quality
description: >
  Analyzes classified review comments from the JIRA Agent Dashboard to find
  recurring patterns in PR feedback, then opens PRs to improve the AI skills
  and repo configuration that generate those PRs. Run this weekly or whenever
  you want to close the feedback loop between human reviewers and the
  jira-solve / address-review-comments automation. Use this skill proactively
  when someone mentions improving PR quality, reducing review comments,
  tuning AI skills, or asks "why do our PRs keep getting the same feedback?"
---

# Improve AI Quality

## Purpose

The HyperShift team runs a periodic job (`/jira:solve --ci`) that picks up
Jira tickets, generates code fixes with Claude, and opens pull requests.
Human reviewers and CodeRabbit then leave comments on those PRs. This skill
closes the feedback loop: it reads those classified comments, identifies the
most common and costly patterns, and opens PRs to fix the root causes in the
skills and configuration that generated the original code.

**Success metric**: fewer review comments per PR over time, because
jira-solve gets things right the first time.

## Environment

The dashboard API is reachable in-cluster or via the public route. The
fetch script auto-detects which is available. GitHub credentials (`gh` CLI)
must be available in the environment.

## Repos and Their Improvement Targets

| Repo | Clone URL | What to improve |
|------|-----------|-----------------|
| **hypershift** | `github.com/openshift/hypershift` | `CLAUDE.md`, `.claude/rules/`, `.claude/skills/` — project-level instructions that guide every Claude session working on the repo. Any file in the repo is fair game if the pattern warrants it. |
| **ai-helpers** | `github.com/openshift-eng/ai-helpers` | `plugins/jira/commands/solve.md` — the jira-solve command, `plugins/utils/commands/address-reviews.md` — the address-review-comments command, `plugins/code-review/` — code review skills. Other plugin skills and commands can also be modified if the analysis points to them. |

## Workflow

### Step 1: Collect comments

Run the fetch script to download and preprocess comments from the dashboard
API. The script auto-detects whether it's running in-cluster or locally,
fetches comments for the date range, truncates long bodies, and adds a
summary header with counts by severity and topic.

```bash
SKILL_DIR="$(dirname "$(readlink -f "$0")" 2>/dev/null || echo ".")"
OUTFILE=$("${SKILL_DIR}/scripts/fetch-comments.sh" "${FROM}" "${TO}")
```

If the script is not available, fall back to a direct curl + jq call:
```bash
BASE_URL="https://dashboard-public-jira-agent-dashboard.apps.jira-agent-scraper.brcox.hypershift.devcluster.openshift.com"
curl -sf "${BASE_URL}/api/comments/summary?from=${FROM}&to=${TO}" \
  | jq '{meta: {total: length, by_severity: (group_by(.severity) | map({key: .[0].severity, count: length}) | from_entries)}, comments: [.[] | {id, severity, topic, author, pr_url, body: (.body[:200])}]}' \
  > /tmp/dashboard-comments.json
OUTFILE=/tmp/dashboard-comments.json
```

Then read the output file. The JSON has two top-level keys:

- `meta`: total count, by_severity counts, by_topic counts, actionable count
- `comments`: array of comment objects with `id`, `severity`, `topic`,
  `author`, `pr_url`, `body` (truncated to 200 chars), `confidence`,
  `ai_classified`, `human_override`

### Step 2: Analyze patterns

Group comments by topic and severity. Focus on the categories that cost the
team the most review effort — that means prioritizing by:

1. **Count of `required_change` comments per topic** — these are the changes
   reviewers insist on before merging. Every one means jira-solve got
   something wrong.
2. **Count of `suggestion` comments per topic** — these indicate room for
   improvement even if the PR was mergeable.
3. Ignore `nitpick`, `approval`, and `unclassified` — they're noise for
   this analysis.

For each significant pattern (3+ comments in the period), read the actual
comment bodies to understand the specific feedback. Look for:

- **Recurring phrasing**: multiple reviewers saying the same thing in
  different words → a missing rule
- **File/package clusters**: comments concentrated in certain areas →
  skill needs domain-specific guidance
- **Reviewer-specific patterns**: one reviewer consistently flags the same
  class of issue → that reviewer's expertise should be codified

### Step 3: Map patterns to fixes

For each pattern identified, determine which file in which repo needs
updating. Use this decision tree:

**Is the issue about how jira-solve approaches the problem?**
(e.g., doesn't write tests, skips verification, wrong commit structure)
→ Fix `ai-helpers/plugins/jira/commands/solve.md`

**Is the issue about how review comments are addressed?**
(e.g., doesn't check if fix compiles, misunderstands reviewer intent)
→ Fix `ai-helpers/plugins/utils/commands/address-reviews.md`

**Is the issue about HyperShift code conventions or patterns?**
(e.g., wrong error handling pattern, missing owner references, wrong
logging style, missing test assertions)
→ Fix `hypershift/CLAUDE.md` or add a rule in `hypershift/.claude/rules/`

**Is the issue about code review quality?**
(e.g., the AI review misses certain classes of bugs)
→ Fix `ai-helpers/plugins/code-review/`

When in doubt, prefer adding a rule to `hypershift/CLAUDE.md` — it's the
most broadly applicable target and the easiest to test.

### Step 4: Clone repos and implement fixes

For each repo that needs changes:

```bash
WORKDIR=$(mktemp -d)
cd "$WORKDIR"
gh repo clone openshift/hypershift -- --depth=1
# or
gh repo clone openshift-eng/ai-helpers -- --depth=1
```

Create a feature branch:
```bash
git checkout -b improve-ai-quality-$(date +%Y%m%d)
```

Before making changes, **read the current content** of each target file to
understand what rules and instructions already exist. Do not duplicate
existing guidance. Instead:

- **Strengthen** existing rules that are too vague (e.g., "write tests" →
  "write table-driven unit tests using gomega assertions for every new
  exported function")
- **Add** new rules for patterns that aren't covered at all
- **Add examples** to existing rules that reviewers keep flagging — a
  concrete before/after example is worth ten sentences of instruction

### Step 5: Write clear, specific improvements

Each improvement must be:

- **Specific**: not "be careful with error handling" but "always wrap errors
  with `fmt.Errorf("context: %w", err)` — never discard the original error"
- **Actionable**: the AI can follow it mechanically on the next run
- **Evidenced**: every change must trace back to specific review comments
- **Minimal**: don't rewrite entire files. Make surgical additions that
  address the specific pattern.

For each change you make, record these four fields for use in the PR
description (Step 6):

1. **What changed**: the file and the specific addition/edit (a one-line
   summary, not the full diff — the diff is in the PR itself)
2. **Pattern**: what recurring review feedback this addresses (topic, count,
   severity breakdown)
3. **Evidence**: quote 2–3 representative review comments verbatim (with
   PR URL) that demonstrate the pattern. Pick the clearest examples.
4. **Reasoning**: why this specific change to this specific file will
   prevent the pattern. Connect the dots — the reviewer should be able to
   read this and think "yes, that makes sense."

### Step 6: Open PRs

For each repo with changes, the PR body must make the case for every
change with evidence. A reviewer reading the PR should understand:
- What data was analyzed
- What patterns emerged
- Why each change addresses a real, recurring problem
- What specific comments prove the problem exists

```bash
git add -A
git commit -m "chore: improve AI skill quality based on review feedback

Analyzed $(COUNT) review comments from $(FROM) to $(TO).
Top patterns addressed:
$(PATTERNS)

Signed-off-by: JIRA Agent <jira-agent@hypershift.dev>
Commit-Message-Assisted-by: Claude (via Claude Code)"

git push -u origin improve-ai-quality-$(date +%Y%m%d)

gh pr create --base main \
  --title "NO-JIRA: Improve AI skill quality based on review comment analysis" \
  --body "$(cat <<'EOF'
## Summary

Automated analysis of review comments from the JIRA Agent Dashboard
identified recurring patterns in PR feedback. This PR updates AI skills
and configuration to address the most common issues.

### Analysis Period
${FROM} to ${TO} — ${COUNT} comments analyzed, ${ACTIONABLE} actionable
(after filtering nitpick/approval/unclassified)

### Changes

For each change, list:

#### Change N: [one-line summary of what was added/modified]

**File:** `path/to/file`

**Pattern:** [topic] — N comments (X required_change, Y suggestion)

**Evidence:**
> "[quoted reviewer comment 1]"
> — @reviewer, [PR URL]

> "[quoted reviewer comment 2]"
> — @reviewer, [PR URL]

**Reasoning:** [2-3 sentences explaining why this change will prevent
the pattern. Connect the evidence to the fix.]

---

(Repeat for each change)

## Test Plan
- [ ] Verify existing skills still parse correctly
- [ ] Next week's jira-solve runs should show fewer comments in the
      addressed categories
- [ ] Compare comment counts week-over-week in the dashboard

Generated with [Claude Code](https://claude.com/claude-code)
via improve-ai-quality skill
EOF
)" --draft
```

### Step 7: Report

Print a summary:
- How many comments were analyzed
- What patterns were found (topic, count, severity breakdown)
- What changes were made and in which repos
- Links to the PRs opened
- Trend comparison: if this isn't the first run, compare this week's
  pattern counts to previous weeks to show whether past improvements
  are working

## Arguments

- `$1`: Start date (YYYY-MM-DD). Defaults to 7 days ago.
- `$2`: End date (YYYY-MM-DD). Defaults to today.

## Example invocation

```
/improve-ai-quality 2026-05-01 2026-06-01
```

Or for the default last-7-days window:
```
/improve-ai-quality
```
