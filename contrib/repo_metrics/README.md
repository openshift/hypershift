# Repository Metrics

Tools for analyzing repository metrics and statistics.

## Setup

This project uses [uv](https://github.com/astral-sh/uv) for dependency management.

```bash
# Install uv if you don't have it
curl -LsSf https://astral.sh/uv/install.sh | sh

# Install dependencies (to be run inside the directory where pyproject.toml is)
uv sync --dev
```

## Tools

### PR Report Generator

Generates comprehensive PR reports for openshift/hypershift and openshift-eng/ai-helpers repositories. Defaults to the last 7 days but supports custom date ranges.

**Features:**
- Fetches all repository contributors (194 total)
- Uses GitHub GraphQL API for efficient PR fetching
- Parallel async API calls with aiohttp
- Filters PRs by merge date
- Extracts complete PR timeline (draft→ready→merge)
- Identifies reviewers and approvers
- Groups PRs by OCPSTRAT parent
- Generates timing metrics and statistics
- Auto-generates OCPSTRAT impact statements

**Performance:** ~2 seconds (90-180x faster than previous agent-based approach)

**Usage:**

```bash
# Install aiohttp dependency
pip install aiohttp

# Run with default (7 days ago)
python3 weekly_pr_report.py

# Generate report since specific date
python3 weekly_pr_report.py 2025-11-13
```

**Via Claude slash command:**

```bash
# From repository root
/pr-report 2025-11-13
```

**Output:**

```text
Generating PR report since: 2025-11-13
Using async (aiohttp) mode

Fetching HyperShift contributors...
Found 194 HyperShift contributors
Fetching PRs from repositories...
Found 27 PRs (24 hypershift, 3 ai-helpers)
Found 2 unique Jira tickets
Loaded Jira hierarchy cache with 2 entries
Generating report...
Report written to /tmp/weekly_pr_report_fast.md
Raw data saved to /tmp/hypershift_pr_details_fast.json

Done in 2.02 seconds!
```

**Files generated:**
- `/tmp/weekly_pr_report_fast.md` - Comprehensive markdown report
- `/tmp/hypershift_pr_details_fast.json` - Raw PR data in JSON format

### AI-Assisted Commits Analyzer

Analyzes git commits to identify those assisted by AI tools (Claude, GPT, etc.).

**Usage:**

```bash
# Run with default (last 2 weeks)
uv run python ai_assisted_commits.py

# Analyze last N commits
uv run python ai_assisted_commits.py -n 100

# Analyze since relative date
uv run python ai_assisted_commits.py --since "1 month ago"

# Analyze since specific date
uv run python ai_assisted_commits.py --since "2025-09-01"
```

**Note:** You cannot specify both `--since` and `-n/--max-count` at the same time.

**Output:**

```text
=== AI-Assisted Commits Report (2 weeks ago) ===

Absolute Numbers:
  Total commits: 48
    - Merge commits: 25
    - Non-merge commits: 23
  AI-assisted commits: 13
    - AI-assisted non-merge: 11
    - AI-assisted merge: 2

Percentages:
  Overall AI-assisted: 27.1% (13/48)
  Non-merge AI-assisted: 47.8% (11/23)
```

## Testing

```bash
# Run all tests
uv run pytest

# Run with verbose output
uv run pytest -v

# Run specific test file
uv run pytest test_ai_assisted_commits.py -v
```
