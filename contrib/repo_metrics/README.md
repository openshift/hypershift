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
