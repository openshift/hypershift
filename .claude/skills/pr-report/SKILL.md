---
name: pr-report
description: >
  Generate comprehensive PR reports for HyperShift and related repositories with metrics,
  impact analysis, and optional deep code analysis. Use when you need a weekly or periodic
  summary of merged PRs, contributor activity, strategic initiative progress, breaking
  changes, or a narrative blog-style progress report. Supports date ranges, deep diff
  analysis, Jira enrichment, collaboration reports, and MkDocs blog post generation.
---

# PR Report Generator

Generate comprehensive PR reports for openshift/hypershift, openshift-eng/ai-helpers,
openshift/enhancements, and openshift/release repositories.

## Usage

```
/skill:pr-report                                              # Last 7 days (default)
/skill:pr-report --start 2026-02-05                           # From date to today
/skill:pr-report --start 2026-02-05 --end 2026-02-12         # Specific date range
/skill:pr-report --start 2026-02-05 --end 2026-02-12 --deep  # With deep code analysis
/skill:pr-report --start 2026-02-05 --end 2026-02-12 --deep --progress-report
/skill:pr-report --start 2026-02-05 --end 2026-02-12 --deep --breaking-changes
/skill:pr-report --start 2026-05-14 --end 2026-06-22 --deep --progress-report --blog
```

**Parameters:**
- `--start` (optional): Start date in YYYY-MM-DD format. Defaults to 7 days ago.
- `--end` (optional): End date in YYYY-MM-DD format. Defaults to today.
- `--deep` (optional): Enable deep analysis mode — fetches and analyzes actual code diffs.
- `--progress-report` (optional): Generate a narrative blog-style progress report. **Requires `--deep`.**
- `--breaking-changes` (optional): Generate a breaking change assessment for SREs. **Requires `--deep`.**
- `--blog` (optional): Generate a Material-styled blog post in `docs/content/blog/`. **Requires `--progress-report`.**
- `--output-dir` (optional): Directory for output files. If not specified, ask the user.

## What This Skill Generates

### 1. Fast Data Report (automated)
- Fetches merged PRs from all repositories
- Filters openshift/release PRs to HyperShift-related paths only
- Filters openshift-eng/ai-helpers and openshift/enhancements PRs to HyperShift contributors
- Queries Jira for ticket hierarchy (Epic, OCPSTRAT linkage)
- Generates metrics (timing, reviewers, merge patterns)

### 2. Impact Analysis Report (LLM-generated)
- Analyzes PR changes to assess actual impact
- Groups work by themes and strategic initiatives
- Highlights notable changes, risks, and cross-repo dependencies

### 3. Deep Code Analysis (--deep only)
- Fetches actual code diffs for selected PRs
- Identifies breaking changes, API modifications, test coverage
- Verifies alignment between PR descriptions and actual changes

### 4. Progress Report (--progress-report, narrative blog post)
- Narrative technical blog post (Dolphin Emulator style)
- Problem-first storytelling with technical depth
- Credits contributors by GitHub handle
- Thematic grouping of related changes

### 5. Breaking Change Report (--breaking-changes)
- All PRs with breaking changes, API modifications, or behavioral shifts
- Links each to Jira tickets with full hierarchy
- Impact scope per customer segment and usage pattern
- Actionable recommendations (revert, fix, document, accept)

### 6. Collaboration Report (always with --deep)
- Groups contributors into clusters based on review relationships
- Identifies bridge nodes connecting clusters

### 7. Blog Post (--blog)
- MkDocs Material blog post with grid stat cards, admonitions, and icons
- Sortable contributor table with per-repo linked PR counts
- Updates `docs/content/blog/index.md` and `docs/mkdocs.yml` nav

## Output Files

| File | Description |
|------|-------------|
| `$OUTPUT_DIR/weekly_pr_report_fast.md` | Data-focused report with metrics |
| `$OUTPUT_DIR/hypershift_pr_details_fast.json` | Raw PR data in JSON |
| `$OUTPUT_DIR/hypershift_pr_summary.json` | Compact summary for LLM analysis |
| `$OUTPUT_DIR/weekly_pr_report_impact.md` | Impact analysis report |
| `$OUTPUT_DIR/pr_scored.json` | Ranked PR list for deep analysis |
| `.work/pr_deep/*.json` | Per-PR data with diffs (deep mode) |
| `.work/pr_deep/*_analysis.json` | Per-PR analysis results (deep mode) |
| `.work/pr_deep_aggregated.json` | Aggregated analysis findings (deep mode) |
| `$OUTPUT_DIR/hypershift_progress_report_YYYY-MM-DD.md` | Narrative progress report |
| `$OUTPUT_DIR/breaking_changes_report_YYYY-MM-DD.md` | Breaking change assessment |
| `$OUTPUT_DIR/collaboration_report_YYYY-MM-DD.md` | Contributor collaboration clusters |
| `docs/content/blog/YYYY-MM-progress-report.md` | Material-styled blog post |

## Implementation

### Step 0: Validate Arguments

- `--progress-report` requires `--deep`. Stop with error if missing.
- `--blog` requires `--progress-report`. Stop with error if missing.
- `--breaking-changes` requires `--deep`. Stop with error if missing.

### Step 1: Run the Python Script to Fetch PRs

```bash
python3 contrib/repo_metrics/weekly_pr_report.py $SINCE_DATE --end $END_DATE --output-dir $OUTPUT_DIR
```

When `--blog` is active, include `--blog-data` to generate `$OUTPUT_DIR/blog_data.json`.

### Step 2: Jira Enrichment (Conditional)

**If script output shows "Fetching Jira data via REST API..."** (JIRA_EMAIL + JIRA_TOKEN set):
- Jira hierarchy was populated automatically. Skip to Step 3.

**If script output shows "JIRA_EMAIL/JIRA_TOKEN not set, loading from cache"**:
- Extract unique tickets from PR data
- Query Jira for each ticket to get hierarchy
- **Fields to request:** `summary,description,parent,issuetype,customfield_10978,customfield_10979,customfield_10980,issuelinks,labels,priority,status`
- **Key fields (Jira Cloud at redhat.atlassian.net):**
  - `parent` = native hierarchy field (includes key, summary, issuetype with hierarchyLevel)
  - `customfield_10978` = SFDC Cases Counter
  - `customfield_10979` = SFDC Cases Links
  - `customfield_10980` = SFDC Cases Open
- **Hierarchy:** Walk `parent` chain: Story/Bug (level 0) → Epic (level 1) → Feature (level 2, was OCPSTRAT)
- Save to `$OUTPUT_DIR/jira_hierarchy.json`
- Re-run the Python script with enriched data

### Step 3: Generate Impact Analysis Report

Read `$OUTPUT_DIR/hypershift_pr_details_fast.json` and `$OUTPUT_DIR/jira_hierarchy.json`, then generate `$OUTPUT_DIR/weekly_pr_report_impact.md`.

**Report structure:**

```markdown
# HyperShift Weekly Impact Report

**Period:** YYYY-MM-DD to YYYY-MM-DD
**Audience:** Project contributors and followers

## Summary
[2-3 paragraphs on progress across all repositories]

## Strategic Initiatives Progress
### OCPSTRAT-XXXX: [Initiative Name]
**Status:** [Active development / Nearing completion / Maintenance]
**This Week's Progress:** [Bullet points]
**PRs:** #XXX, #YYY, #ZZZ

## Repository Highlights
### openshift/hypershift
#### Platform Support
#### Control Plane
#### Bug Fixes
#### Testing & Quality

### openshift-eng/ai-helpers
### openshift/release

## Notable PRs
[Deep-dive on 3-5 highest-scored PRs]

## Risks & Breaking Changes
## Dependencies & Cross-Repo Changes

## Metrics Snapshot
| Metric | Value |
|--------|-------|

## Coming Up
```

**Guidelines:**
1. Audience is contributors and followers — technical language with "why" explanations
2. Assess actual impact from PR descriptions
3. Group related work together
4. Highlight cross-repo dependencies
5. Be specific about platforms (AWS, Azure, GCP, KubeVirt, Agent)
6. Call out breaking changes prominently
7. Recognize contributors by @handle

### Step 4: Deep Code Analysis (--deep only)

#### 4a: Interactive PR Selection

Score each PR:

| Criterion | Points |
|-----------|--------|
| Enhancement proposal | +200 |
| Jira Priority: Critical/Blocker | +100 |
| Jira Priority: Major | +50 |
| Jira Priority: Normal | +20 |
| Jira Priority: Minor | +10 |
| SDK/API/Migration work | +30 |
| Feature work | +15 |
| Bug fix (OCPBUGS) | +10 |
| Has any Jira ticket | +5 |
| Manual CI change | +10 |

Ask the user which PRs to analyze:
- "All PRs (X total)"
- "High-value selection (15-20 PRs)" — auto-select top by score
- "Bug fixes only (Z PRs)"
- "Custom selection" — show annotated PR table for selection

#### 4b: Fetch Diffs

```bash
python3 contrib/repo_metrics/weekly_pr_report.py "$SINCE_DATE" \
    --deep openshift/hypershift#7709 openshift/release#74707 ...
```

Creates per-PR JSON files in `.work/pr_deep/`.

#### 4c: Analyze PRs with Subagents

For each JSON file in `.work/pr_deep/`, delegate to a subagent in batches of 3-5:

**Subagent prompt:**
```
Read .work/pr_deep/<key>.json and analyze the PR diff.

Output the analysis JSON directly in your response. Do NOT write files.

CRITICAL: Use the "author" field from the input JSON for attribution.

Produce JSON with:
{
  "repo", "number", "author", "summary",
  "actual_changes": [...],
  "alignment_with_description": "matches" | "partial" | "misleading",
  "breaking_changes": [...],
  "test_coverage": "...",
  "api_changes": true | false,
  "files_changed": {"total": N, "by_type": {...}},
  "notable_observations": [...],
  "impact_level": "high" | "medium" | "low",
  "impact_statement": "..."
}
```

After each subagent completes, write the extracted JSON to `.work/pr_deep/<key>_analysis.json`.

#### 4d: Aggregate Findings

Combine all `*_analysis.json` into `.work/pr_deep_aggregated.json`.

#### 4e: Enhanced Impact Report

Re-generate `weekly_pr_report_impact.md` with deep findings — use `actual_changes`, highlight `breaking_changes`, note alignment issues.

### Step 5: Generate Progress Report (--progress-report only)

Write to `$OUTPUT_DIR/hypershift_progress_report_YYYY-MM-DD.md`.

**Data sources:**
- `$OUTPUT_DIR/hypershift_pr_details_fast.json` (including `author` field)
- `$OUTPUT_DIR/jira_hierarchy.json`
- `.work/pr_deep_aggregated.json` and per-PR analysis files
- `$OUTPUT_DIR/weekly_pr_report_impact.md`

**CRITICAL:** Always use the `author` field from PR data for attribution. Never guess.

**Writing Style:**
1. Problem-first storytelling — explain the problem, why it mattered, how it's fixed
2. Conversational but authoritative tone
3. Technical depth with accessibility
4. Historical context for changes
5. Credit contributors by @handle from the `author` field
6. Thematic grouping over chronological listing
7. Highlight interesting edge cases and trade-offs
8. Select 5-8 most impactful changes for deep narrative; minor fixes in "smaller changes"

**Structure:**

```markdown
# HyperShift Progress Report: [Month Day - Day, Year]

[Opening: PR count, major themes, one hook]

---

## [Narrative Section Title]
**By @author -- [PR #XXXX](url)**
[3-8 paragraphs: problem, approach, technical details, edge cases]

---

## Smaller Changes Worth Noting
- **[Title]** (@author, [PR #XXXX](url)): One-sentence description.
```

### Step 5b: Breaking Change Report (--breaking-changes only)

Write to `$OUTPUT_DIR/breaking_changes_report_YYYY-MM-DD.md`.

**Target audience:** Management cluster operators and senior SREs.

**Include PRs with:** non-empty `breaking_changes`, `api_changes: true`, behavioral changes.

**Severity classification:**

| Severity | Criteria |
|----------|----------|
| Critical | Data loss risk, cluster unavailability, silent corruption |
| High | Breaks existing integrations or procedures |
| Medium | Changes observable behavior, has workarounds |
| Low | Minor change, unlikely to affect most operators |

### Step 5c: Collaboration Report (always with --deep)

Write to `$OUTPUT_DIR/collaboration_report_YYYY-MM-DD.md`. One paragraph per cluster of related contributors, plus bridge nodes.

### Step 5d: Blog Post (--blog only)

#### 5d-1: Generate blog data

Verify `$OUTPUT_DIR/blog_data.json` exists (generated in Step 1 with `--blog-data`). Spot-check `release_only` contributors.

#### 5d-2: Transform progress report to blog post

Write to `docs/content/blog/YYYY-MM-progress-report.md`.

**Insert pre-rendered markdown from `blog_data.json`:**
- `markdown.stats_cards` → after H1 title
- `markdown.metrics_table` → in "By the Numbers" section
- `markdown.top_reviewers_table` → after metrics table
- `markdown.contributor_table` → in "Contributors" section

**Content rules:**
- Preserve all PR links and em dashes
- Title: `{Month} {Year} Progress Report`
- Add Material icons to section headings
- Convert only clearly-marked callout paragraphs to admonitions
- Link `@username` mentions to GitHub profiles
- Add "By the Numbers", "Contributors", and "What's Next" sections

**Sensitive content filtering (blog is public):**
- S360 references → "compliance"
- Remove SFDC case references
- No internal-only Jira links
- No specific customer names

#### 5d-3: Update blog index and mkdocs.yml

Add card entry to top of `docs/content/blog/index.md`. Add nav entry to `docs/mkdocs.yml` Blog section.

#### 5d-4: Verify

Run `make docs-aggregate`. Suggest `cd docs && mkdocs serve` for preview.

### Step 6: Present Results

Summarize what was generated, file locations, key highlights, and next steps.

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `JIRA_EMAIL` | No | Atlassian account email (enables direct API mode) |
| `JIRA_TOKEN` | No | Jira Cloud API token |
| `JIRA_URL` | No | Jira Cloud URL (default: `https://redhat.atlassian.net`) |
| `GITHUB_TOKEN` | No | GitHub token (falls back to `gh auth token`) |

## Notes

- Requires `aiohttp` Python package: `pip install aiohttp`
- Falls back to synchronous mode if aiohttp unavailable
