---
description: Process open PR review comments and push fixes
user_invocable: true
---

Use the `author-code-review` agent to:

1. Find all my open PRs in this repository
2. Analyze review comments (including CoderabbitAI)
3. Identify actionable code change requests
4. Implement the requested changes
5. Run verification (`make verify`, `make test`)
6. Commit and push fixes

Run in interactive mode - confirm with me before making changes and before pushing.

$ARGUMENTS
