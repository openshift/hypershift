---
description: "PR Report Generator"
argument-hint: "[--start YYYY-MM-DD] [--end YYYY-MM-DD] [--deep] [--progress-report] - Date range with optional deep analysis and progress report"
---

# PR Report Generator

Generate comprehensive PR reports for openshift/hypershift, openshift-eng/ai-helpers, and openshift/release repositories.

## Usage

```
/pr-report                                    # Last 7 days (default)
/pr-report --start 2026-02-05                 # From date to today
/pr-report --start 2026-02-05 --end 2026-02-12  # Specific date range
/pr-report --start 2026-02-05 --end 2026-02-12 --deep  # With deep code analysis
/pr-report --start 2026-02-05 --end 2026-02-12 --progress-report       # With narrative progress report
/pr-report --start 2026-02-05 --end 2026-02-12 --deep --progress-report # Deep analysis + progress report
```

**Parameters:**

- `--start` (optional): Starting date in YYYY-MM-DD format (e.g., `2026-02-05`). Defaults to 7 days ago.
- `--end` (optional): Ending date in YYYY-MM-DD format (e.g., `2026-02-12`). Defaults to today.
- `--deep` (optional): Enable deep analysis mode that fetches and analyzes actual code diffs.
- `--progress-report` (optional): Generate a narrative blog-style progress report (Dolphin Emulator style).

## What This Command Does

This command generates **two reports** (with two optional additions):

### 1. Fast Data Report (automated)
- Fetches merged PRs from all three repositories
- Filters openshift/release PRs to only HyperShift-related paths
- Filters openshift-eng/ai-helpers PRs to HyperShift contributors
- Queries Jira for ticket hierarchy (Epic, OCPSTRAT linkage)
- Generates metrics (timing, reviewers, merge patterns)

### 2. Impact Analysis Report (LLM-generated)
- Analyzes PR changes to assess actual impact
- Groups work by themes and strategic initiatives
- Provides context for project contributors and followers
- Highlights notable changes, risks, and cross-repo dependencies

### 3. Deep Code Analysis (--deep mode only)
- Fetches actual code diffs for selected PRs
- Analyzes what the code actually changed (not just descriptions)
- Identifies breaking changes, API modifications, test coverage
- Verifies alignment between PR descriptions and actual changes

### 4. Progress Report (--progress-report, narrative blog post)
- Narrative technical blog post in the style of Dolphin Emulator progress reports
- Problem-first storytelling with technical depth
- Credits contributors by GitHub handle
- Thematic grouping of related changes

## Output Files

| File | Description |
|------|-------------|
| `/tmp/weekly_pr_report_fast.md` | Data-focused report with metrics and PR listings |
| `/tmp/hypershift_pr_details_fast.json` | Raw PR data in JSON format |
| `/tmp/weekly_pr_report_impact.md` | LLM-generated impact analysis for contributors |
| `.work/pr_deep/*.json` | (--deep mode) Per-PR data with diffs for analysis |
| `.work/pr_deep/*_analysis.json` | (--deep mode) Per-PR analysis results |
| `.work/pr_deep_aggregated.json` | (--deep mode) Aggregated analysis findings |
| `/tmp/hypershift_progress_report_YYYY-MM-DD.md` | (--progress-report) Narrative progress report (blog-style) |

## Implementation

### Step 1: Run the Python script to fetch PRs

Parse the arguments and run the script. The script accepts:
- Positional `since_date` (start date)
- `--end` flag for end date

```bash
# Parse ARGUMENTS which may contain: --start YYYY-MM-DD --end YYYY-MM-DD --deep
# Example: python3 contrib/repo_metrics/weekly_pr_report.py 2026-02-05 --end 2026-02-12

python3 contrib/repo_metrics/weekly_pr_report.py $ARGUMENTS
```

Note: The script's positional argument is the start date. The `--start` flag in the skill
is mapped to this positional argument when invoking the script.

### Step 2: Jira Enrichment (Conditional)

Check the script output to determine if Jira data was fetched directly:

**If script output shows "Fetching Jira data via REST API..."** (JIRA_TOKEN was set):
- Jira hierarchy was populated automatically by the script
- Skip to Step 3 (Impact Analysis Report)

**If script output shows "JIRA_TOKEN not set, loading from cache"**:
- Extract unique tickets from PR data and query Jira using MCP tools
- Use `mcp__atlassian-mcp__jira_get_issue` for EACH ticket (in parallel)
- **Fields to request:** `summary,description,parent,customfield_12311140,customfield_12313140,issuelinks,labels,priority,status`
- **Key custom fields:**
  - `customfield_12311140` = Epic Link field
  - `customfield_12313140` = OCPSTRAT Parent (e.g., "OCPSTRAT-2426")
- Build hierarchy: ticket → Epic → OCPSTRAT
- Save to `/tmp/jira_hierarchy.json`
- Re-run the Python script to regenerate report with enriched data

### Step 3: Generate Impact Analysis Report

Read `/tmp/hypershift_pr_details_fast.json` and `/tmp/jira_hierarchy.json`, then generate a rich impact analysis report for project contributors and followers.

**Write the report to `/tmp/weekly_pr_report_impact.md`** following this structure:

```markdown
# HyperShift Weekly Impact Report

**Period:** YYYY-MM-DD to YYYY-MM-DD
**Audience:** Project contributors and followers

## Summary

[2-3 paragraphs summarizing the week's progress across all repositories. Highlight major themes,
significant changes, and overall project momentum. Write in a tone suitable for developers and
community members following the project.]

## Strategic Initiatives Progress

[For each OCPSTRAT initiative with activity this week, provide:]

### OCPSTRAT-XXXX: [Initiative Name]

**Status:** [Active development / Nearing completion / Maintenance]

**This Week's Progress:**
- [Bullet points describing meaningful progress, not just PR titles]
- [Focus on what capability was added/fixed and why it matters]

**PRs:** #XXX, #YYY, #ZZZ

---

## Repository Highlights

### openshift/hypershift

[Group PRs by theme and explain their collective impact:]

#### Platform Support
- **AWS:** [What changed and why it matters]
- **Azure/ARO:** [What changed and why it matters]
- **GCP:** [What changed and why it matters]
- **KubeVirt:** [What changed and why it matters]

#### Control Plane
[Significant changes to CPO, HO, or core controllers]

#### Bug Fixes
[Notable bugs resolved, grouped by severity/impact]

#### Testing & Quality
[Test improvements, CI fixes, coverage additions]

### openshift-eng/ai-helpers

[Summarize AI tooling improvements and their impact on developer workflow]

### openshift/release

[Summarize CI configuration changes grouped by category:]

#### CI Efficiency
[Job optimizations, resource adjustments]

#### Test Coverage
[New tests, validation improvements]

#### Automation
[Bot improvements, workflow automation]

---

## Notable PRs

[Deep-dive on 3-5 high-impact PRs. For each:]

### PR #XXXX: [Title]

**Repository:** [repo]
**Author:** @username
**Why it matters:** [2-3 sentences explaining the actual impact of this change for users/developers]

**What changed:**
- [Technical summary of the key changes]
- [Any breaking changes or migration notes]

**Related:** [Links to related PRs, issues, or documentation]

---

## Risks & Breaking Changes

[List any changes that could affect users or downstream consumers:]

- **[Component]:** [Description of risk/change and mitigation]

## Dependencies & Cross-Repo Changes

[Highlight changes that span multiple repositories or require coordinated updates]

---

## Metrics Snapshot

| Metric | Value |
|--------|-------|
| Total PRs merged | X |
| Unique contributors | X |
| Avg time to merge | X hours |
| Most active reviewer | @username (X PRs) |

## Coming Up

[If visible from PR descriptions or Jira tickets, mention work in progress or upcoming features]
```

**Guidelines for Impact Analysis:**

1. **Audience is contributors and followers** - Use technical language appropriate for developers, but explain the "why" not just the "what"

2. **Assess actual impact** by reading PR descriptions and understanding:
   - What problem does this solve?
   - Who benefits from this change?
   - Does this enable new capabilities or fix broken functionality?

3. **Group related work** - Multiple PRs often contribute to one logical change; group them together

4. **Highlight cross-repo dependencies** - Changes in openshift/release often enable or support changes in hypershift

5. **Be specific about platforms** - HyperShift supports multiple platforms (AWS, Azure, GCP, KubeVirt, Agent); call out platform-specific work

6. **Call out breaking changes** - Any API changes, behavior changes, or deprecations should be prominently noted

7. **Recognize contributors** - Mention authors of significant work

**Example Impact Transformations:**

| PR Title | Impact Analysis |
|----------|-----------------|
| "OCPBUGS-60707: Fix user-ca-bundle cleanup when additionalTrustBundle is removed" | Fixes a bug where custom CA certificates persisted in guest clusters after being removed from HostedCluster spec. Operators managing trust bundles will now see expected cleanup behavior. |
| "GCP-216: feat(nodepool): add GCP platform support" | Adds foundational NodePool support for GCP platform, enabling cluster autoscaling and machine management. This is a key milestone for GCP HyperShift availability. |
| "CNTRLPLANE-2082: hypershift: run conformance directly on the root cluster" | Simplifies CI architecture by running conformance tests on the management cluster instead of nested clusters. Reduces resource requirements and test complexity. |

### Step 4: Deep Code Analysis (--deep mode only)

If `--deep` flag is specified, perform deep code analysis after the standard report:

#### Step 4a: Interactive PR Selection

Present the user with PR selection options using AskUserQuestion:

1. Read `/tmp/hypershift_pr_details_fast.json` and `/tmp/jira_hierarchy.json` to categorize and score PRs.

2. **Score each PR using these criteria** (higher score = more important):

   | Criterion | Points | Description |
   |-----------|--------|-------------|
   | Jira Priority: Critical/Blocker | +100 | Highest priority tickets |
   | Jira Priority: Major | +50 | Major priority tickets |
   | Jira Priority: Normal | +20 | Normal priority tickets |
   | Jira Priority: Minor | +10 | Minor priority tickets |
   | SDK/API/Migration work | +30 | Title contains "sdk", "migrate", "api", or "breaking" |
   | Feature work | +15 | Title contains "feat" or "feature" |
   | Bug fix (OCPBUGS) | +10 | Has OCPBUGS ticket reference |
   | Has any Jira ticket | +5 | Tracked work is more valuable |
   | Manual CI change | +10 | Non-bot author in openshift/release |

   To score PRs, join PR data with Jira hierarchy to get priority:
   ```bash
   # Get priority for a ticket from jira_hierarchy.json
   jq -r '."CNTRLPLANE-1708".priority' /tmp/jira_hierarchy.json
   # Returns: "Critical"
   ```

3. Ask user which PRs to analyze:
   ```
   Options:
   - "All PRs (X total)" - Analyze every PR
   - "High-value selection (15-20 PRs)" - Auto-select top PRs by importance score
   - "Bug fixes only (Z PRs)" - OCPBUGS tickets only
   - "Custom selection" - Choose from annotated PR list
   ```

4. If "High-value selection", sort PRs by score descending and select top 15-20.

5. If "Custom selection", present an **annotated PR list** for user selection:

   **Display format** (output as markdown table for readability):
   ```markdown
   ## Select PRs for Deep Analysis

   | # | Score | Priority | Repo | PR | Title | Topic |
   |---|-------|----------|------|-----|-------|-------|
   | 1 | 105 | Critical | hypershift | #7678 | CNTRLPLANE-2215: feat(aws): migrate S3 to AWS SDK v2 | SDK/API |
   | 2 | 60 | Major | hypershift | #7634 | OCPBUGS-74931: Fix OCM config constant updates | bugfix |
   | 3 | 55 | Major | release | #74707 | CNTRLPLANE-2082: run conformance on root cluster | CI |
   | 4 | 35 | Normal | hypershift | #7658 | CNTRLPLANE-2675: move infrastructure reconciliation | feature |
   | ... | ... | ... | ... | ... | ... | ... |
   ```

   **Topic categories** (derive from title/labels):
   - `SDK/API` - SDK migrations, API changes
   - `bugfix` - OCPBUGS tickets
   - `feature` - New functionality
   - `CI` - CI/testing changes (release repo)
   - `docs` - Documentation
   - `platform:{aws,azure,gcp,kubevirt}` - Platform-specific
   - `cleanup` - Refactoring, maintenance

   Then use AskUserQuestion with multiSelect to let user pick specific PRs by number.

#### Step 4b: Fetch Diffs

Build the PR list based on user selection and run the script with --deep:

```bash
# Build list of PRs in owner/repo#number format
# Example: openshift/hypershift#7709 openshift/release#74707

python3 contrib/repo_metrics/weekly_pr_report.py "$SINCE_DATE" \
    --deep openshift/hypershift#7709 openshift/hypershift#7613 ...
```

This creates per-PR JSON files in `.work/pr_deep/` containing:
- PR metadata (title, author, body, labels)
- Jira hierarchy
- Full diff content (file patches)

#### Step 4c: Analyze PRs with Task Agents

For each JSON file in `.work/pr_deep/`, spawn a Task agent to analyze:

1. List input files: `ls .work/pr_deep/*.json | grep -v '_analysis.json'`

2. Spawn Task agents in batches of 3-5 (parallel execution):

   **Task agent prompt:**
   ```
   Read .work/pr_deep/<key>.json and analyze the PR diff.

   IMPORTANT: Output the analysis JSON directly in your response text.
   Do NOT attempt to write any files. Return the JSON between
   ```json and ``` markers so the parent can extract it.

   CRITICAL: The PR author is in the "author" field of the input JSON.
   Always use this exact value for attribution. Never guess the author
   from the PR description or diff content.

   Produce JSON with these fields:
   {
     "repo": "<repo>",
     "number": <number>,
     "author": "<author from input JSON>",
     "summary": "One sentence describing actual code changes",
     "actual_changes": ["Change 1", "Change 2", ...],
     "alignment_with_description": "matches" | "partial" | "misleading",
     "breaking_changes": ["Breaking change 1", ...] or [],
     "test_coverage": "Description of test changes" or "none",
     "api_changes": true | false,
     "files_changed": {"total": N, "by_type": {"go": X, "yaml": Y, ...}},
     "notable_observations": ["Observation 1", ...],
     "impact_level": "high" | "medium" | "low",
     "impact_statement": "One sentence business/user impact"
   }

   Focus on:
   - What the code actually changes (not just the description)
   - Any breaking changes to APIs or behavior
   - Whether tests are added/modified appropriately
   - Code quality patterns worth noting
   ```

3. After each Task agent completes, extract the JSON from the agent's
   response and write it to `.work/pr_deep/<key>_analysis.json` using
   the Write tool. The agent cannot write files itself.

4. Wait for batch completion, then launch next batch.

#### Step 4d: Aggregate Findings

After all agents complete, aggregate the analysis files:

1. Read all `*_analysis.json` files from `.work/pr_deep/`
2. Combine into `.work/pr_deep_aggregated.json`:
   ```json
   {
     "generated_at": "2026-02-12T15:30:00Z",
     "prs_analyzed": 15,
     "analyses": [ /* all analysis objects */ ],
     "summary": {
       "breaking_changes_count": 0,
       "api_changes_count": 2,
       "high_impact_count": 3
     }
   }
   ```

#### Step 4e: Enhanced Impact Report

When generating `/tmp/weekly_pr_report_impact.md`, incorporate deep findings:

- Use `actual_changes` instead of PR descriptions for accuracy
- Highlight any `breaking_changes` prominently
- Note `alignment_with_description` issues (description vs reality)
- Use `impact_statement` for Notable PRs section

### Step 5: Generate Dolphin-style Progress Report (--progress-report mode only)

If `--progress-report` flag is specified, generate a narrative blog-post-style technical
progress report. Write the report to `/tmp/hypershift_progress_report_YYYY-MM-DD.md`
where YYYY-MM-DD is the end date.

**Data sources:**
- `/tmp/hypershift_pr_details_fast.json` for PR data (including `author` field)
- `/tmp/jira_hierarchy.json` for strategic context
- `.work/pr_deep_aggregated.json` and `.work/pr_deep/*_analysis.json` (if --deep mode)
- `/tmp/weekly_pr_report_impact.md` for the impact analysis already generated

**CRITICAL: Author Attribution**
Always use the `author` field from the PR data JSON for contributor attribution.
Never guess or infer authors from PR descriptions or code content.

**Writing Style Guide:**

1. **Problem-first storytelling**: Don't just say what changed -- explain the problem
   that existed before, why it mattered, and how the change addresses it. Give readers
   the "why" before the "what."

2. **Conversational but authoritative tone**: Write like a knowledgeable engineer
   explaining work to interested peers over coffee. Avoid marketing language and
   buzzwords. Be direct, specific, and occasionally witty.

3. **Technical depth with accessibility**: Go deep on the technical details -- show
   code patterns, explain algorithms, discuss trade-offs. But structure explanations
   so readers can follow even if they're not experts in that specific area.

4. **Historical context**: When relevant, explain what the previous approach was and
   why it's being changed. "The old emptyBucket function relied on X, which had
   problems Y and Z. The new approach does W instead."

5. **Credit contributors by GitHub handle**: Use @username format, sourced from the
   `author` field in the PR data JSON.

6. **Thematic grouping over chronological listing**: Group related changes into
   coherent narratives rather than listing PRs in order. A single section might
   cover 1-3 related PRs that tell one story.

7. **Highlight interesting edge cases and trade-offs**: Readers love learning about
   subtle problems -- TLS ServerName workarounds, race conditions, pre-stable
   dependencies. These are what make the report worth reading beyond just a changelog.

8. **Don't cover everything**: Select 5-8 of the most interesting/impactful changes
   for deep narrative treatment. Minor fixes and routine maintenance can be briefly
   mentioned or grouped into a "smaller changes" section at the end.

**Structure Template:**

```markdown
# HyperShift Progress Report: [Month Day - Day, Year]

[Opening paragraph: 2-3 sentences setting the scene. Total PR count, major themes,
and one hook to draw readers in.]

---

## [Narrative Section Title]
**By @author -- [PR #XXXX](url)**

[3-8 paragraphs telling the story of this change. Start with the problem,
explain the approach, detail interesting technical aspects, note edge cases.]

---

## [Next Section...]
[Repeat for each major topic]

---

## Smaller Changes Worth Noting

[Brief mentions of other work that didn't warrant full sections but should
be acknowledged:]

- **[Title]** (@author, [PR #XXXX](url)): One-sentence description.
- ...
```

### Step 6: Present Results

After generating reports, provide the user with:

1. A brief summary of what was generated
2. Location of all report files
3. Key highlights from the impact analysis
4. (--deep mode) Summary of deep analysis findings
5. (--progress-report mode) Mention the progress report location

## Script Features

The Python script uses:
- **GitHub Contributors API** to fetch HyperShift contributors
- **GitHub GraphQL API** with search queries for efficient PR fetching (`merged:START..END` range syntax)
- **File path filtering** for openshift/release (only PRs touching `hypershift` paths)
- **Parallel async API calls** with aiohttp for maximum performance
- **Jira hierarchy caching** (loads from previous run if available)
- **PR scoring for deep analysis** (`--score` flag) with priority-based ranking
- **Conventional commit parsing** for topic extraction

### Scoring Command

Use `--score` to output a ranked list of PRs for deep analysis selection:

```bash
python3 contrib/repo_metrics/weekly_pr_report.py 2026-02-05 --end 2026-02-12 --score
python3 contrib/repo_metrics/weekly_pr_report.py 2026-02-05 --end 2026-02-12 --score --score-limit 30
```

Output includes:
- Scored PRs table with priority, repo, topic, and title
- JSON file at `/tmp/pr_scored.json` for programmatic use
- Ready-to-use PR list for `--deep` flag

## Notes

- Requires `aiohttp` Python package: `pip install aiohttp`
- Falls back to synchronous mode if aiohttp is not available (slower but functional)

### Jira Integration Modes

**Mode 1: Direct API (Recommended)**
- Set `JIRA_TOKEN` environment variable with a Jira Personal Access Token
- Script fetches Jira data directly via REST API with batch queries
- No MCP tool calls needed - fully automated
- Example: `export JIRA_TOKEN="your-pat-here"`

**Mode 2: MCP Fallback (Interactive use)**
- When `JIRA_TOKEN` is not set, script outputs ticket list only
- Claude uses MCP tools (`mcp__atlassian-mcp__jira_get_issue`) to fetch hierarchy
- Requires Jira MCP server to be configured

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `JIRA_TOKEN` | No | Jira Personal Access Token (enables direct API mode) |
| `JIRA_URL` | No | Jira server URL (defaults to `https://issues.redhat.com`) |
| `GITHUB_TOKEN` | No | GitHub token (falls back to `gh auth token` if not set) |
