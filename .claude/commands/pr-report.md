---
arguments: [YYYY-MM-DD] - Starting date (defaults to 7 days ago)
---

# PR Report Generator

Generate a comprehensive PR report for openshift/hypershift and openshift-eng/ai-helpers repositories.

## Usage

```
/pr-report [since-date]
```

**Parameters:**

- `since-date` (optional): Starting date in YYYY-MM-DD format (e.g., `2025-11-20`). Defaults to 7 days ago.

## What This Command Does

1. **Fetch repository contributors** from openshift/hypershift (all 194 contributors)
2. **Fetch merged PRs** from both repositories since the specified date using GitHub GraphQL API
3. **Filter openshift/ai-helpers PRs** to only include those authored by HyperShift contributors
4. **Query Jira** for each ticket to find Epic and Parent links to OCPSTRAT issues
5. **Gather detailed PR metadata**:
   - Author, reviewers, and approvers
   - Draft status and timeline (when moved from draft to ready)
   - Time from creation to ready
   - Time from ready to merge
6. **Generate comprehensive report** including:
   - PR topic and summary
   - Jira ticket hierarchy (ticket → Epic → OCPSTRAT)
   - Review and approval activity
   - Timing metrics
   - OCPSTRAT groupings
   - Auto-generated impact statements

## Output

The command creates:

- `/tmp/weekly_pr_report_fast.md` - Comprehensive markdown report
- `/tmp/hypershift_pr_details_fast.json` - Raw PR data in JSON format

## Example Usage

```bash
# Report for last week (default)
/pr-report

# Report since specific date
/pr-report 2025-11-06
```

## Expected Output Format

The generated markdown report (`/tmp/weekly_pr_report_fast.md`) should follow this structure:

```markdown
# PR Report: YYYY-MM-DD to YYYY-MM-DD

## Summary

- **Total PRs merged**: X
- **Total contributors**: Y
- **Repositories**: openshift/hypershift (X PRs), openshift-eng/ai-helpers (Y PRs)

## PRs by OCPSTRAT Initiative

### OCPSTRAT-XXXX: [OCPSTRAT Summary]

**Related Epics:**
- EPIC-XXX: [Epic Summary]

#### PR: [PR Title] (#XXXX)
- **Repository**: openshift/hypershift
- **Author**: @username
- **Jira**: TICKET-XXX → EPIC-XXX → OCPSTRAT-XXXX
- **Merged**: YYYY-MM-DD
- **Reviewers**: @user1, @user2
- **Approvers**: @user3
- **Draft Timeline**:
  - Created as draft: YYYY-MM-DD HH:MM
  - Ready for review: YYYY-MM-DD HH:MM
  - Time in draft: X days
- **Review Timeline**:
  - Time to merge after ready: X days
- **Topic**: [Auto-generated topic classification]
- **Summary**: [Brief description of what the PR does]
- **Impact**: [Generated impact statement using Jira context and OCPSTRAT hierarchy]

---

### Uncategorized PRs (No OCPSTRAT Link)

#### PR: [PR Title] (#XXXX)
[Same format as above, but without OCPSTRAT hierarchy]

---

## Metrics

### Time to Merge Distribution
- **Average time from creation to ready**: X.Y days
- **Average time from ready to merge**: X.Y days
- **Fastest PR**: #XXXX (X.Y days)
- **Slowest PR**: #XXXX (X.Y days)

### Top Contributors
1. @username - X PRs
2. @username - Y PRs

### Cross-Repository Contributions
- Contributors working on both repos: X
```

**Key Requirements for Generated Report:**

1. **PRs must be grouped by OCPSTRAT** parent initiative when available
2. **Complete hierarchy** must be shown: Ticket → Epic → OCPSTRAT
3. **Draft timeline** must include creation time, ready time, and duration in draft
4. **Impact statements** should leverage:
   - Ticket summary and description
   - Epic summary for context
   - OCPSTRAT summary for strategic alignment
5. **Topics** should be auto-classified (e.g., "Bug Fix", "Feature Enhancement", "Test Improvement", "Documentation")
6. **Metrics section** must include timing analysis and contributor statistics

## Implementation

This command uses an optimized Python script for fast PR fetching, then enriches the data with Jira hierarchy information.

### Step 1: Run the Python script to fetch PRs

Parse the date argument and run the script:

```bash
SINCE_DATE="$ARGUMENTS"
if [ -z "$SINCE_DATE" ]; then
  SINCE_DATE=$(date -d '7 days ago' +%Y-%m-%d)
fi

echo "Generating PR report since: $SINCE_DATE"
python3 contrib/repo_metrics/weekly_pr_report.py "$SINCE_DATE"
```

### Step 2: Query Jira for ticket hierarchy (in parallel)

Extract all unique Jira ticket IDs from the generated PR data and query Jira for each ticket to build hierarchy and enhance impact statements.

First, extract the unique tickets:

```bash
TICKETS=$(jq -r '.[].jiraTickets[]' /tmp/hypershift_pr_details_fast.json | sort -u)
echo "Found tickets: $TICKETS"
```

Now, for EACH ticket in the list, use `mcp__atlassian-mcp__jira_get_issue` to fetch:

**Fields to request:** `summary,description,parent,customfield_12311140,customfield_12313140,issuelinks,labels,priority,status`

**Key custom fields:**
- `customfield_12311140` = Epic Link field
- `customfield_12313140` = OCPSTRAT Parent (simple string like "OCPSTRAT-2426")

**Build the hierarchy for each ticket:**

1. Query the ticket itself to get summary, description, Epic Link
2. If Epic Link exists (customfield_12311140), query that Epic ticket to get its summary and OCPSTRAT parent
3. Build a JSON structure like:
```json
{
  "TICKET-123": {
    "summary": "Ticket summary text",
    "description": "Full ticket description",
    "epic": "EPIC-456",
    "epicSummary": "Epic summary text",
    "ocpstrat": "OCPSTRAT-789",
    "ocpstratSummary": "OCPSTRAT summary text"
  }
}
```

4. Save ALL ticket hierarchies to `/tmp/jira_hierarchy.json`

**IMPORTANT:** Query all Epics and tickets in PARALLEL using multiple concurrent `mcp__atlassian-mcp__jira_get_issue` calls for maximum performance.

### Step 3: Regenerate report with Jira enrichment

Once Jira hierarchy is saved, re-run the Python script to regenerate the report with enriched impact statements:

```bash
python3 contrib/repo_metrics/weekly_pr_report.py "$SINCE_DATE"
```

The script will now use the Jira data to:
- Show complete ticket → Epic → OCPSTRAT hierarchy
- Generate better impact statements using ticket summaries and OCPSTRAT context
- Group PRs by their OCPSTRAT parent initiatives

## Script Features

The Python script uses:
- **GitHub Contributors API** to fetch all 194 HyperShift contributors
- **GitHub GraphQL API** with search queries for efficient PR fetching (`merged:>=$DATE`)
- **Parallel async API calls** with aiohttp for maximum performance
- **Jira hierarchy caching** (loads from previous run if available)

## Notes

- Requires `aiohttp` Python package: `pip install aiohttp`
- Falls back to synchronous mode if aiohttp is not available (slower but functional)
- Jira hierarchy is loaded from cache (`/tmp/jira_hierarchy.json`) if available
- For full Jira integration, ensure Jira MCP tools are configured
