---
description: "Triage CI failures on a PR, fix blocking issues, retest flaky tests"
arguments: "[PR_NUMBER] [watch until green] - PR number (optional, defaults to current branch's PR) and optional watch mode trigger"
user_invocable: true
---

Invoke the `ci-triage` agent using the Task tool with `subagent_type="ci-triage"`.

Pass the following context to the agent:
- PR number: Parse from $ARGUMENTS if provided, otherwise detect from current branch
- Execution mode: If $ARGUMENTS contains "watch", "until green", "until all pass", or "keep trying", use watch mode

## What It Does

1. Fetches all CI check statuses
2. Categorizes failures into Tier 1 (blocking) and Tier 2 (e2e)
3. If Tier 1 tests fail (verify, unit, security, docs-preview):
   - Analyzes logs to find root cause
   - Fixes the issues locally
   - Commits and pushes the fix
4. If only Tier 2 (e2e) tests fail:
   - Checks if failures are flaky (infrastructure issues, timeouts)
   - If flaky, comments `/retest-required` on the PR
   - If real failure, reports details for investigation

## Execution Modes

**Single Pass (default):** Run once, fix/retest, report status

**Watch Mode:** Run continuously until all tests pass
- Polls CI status every 2-3 minutes
- Automatically fixes new failures and retests flaky tests
- Exits when all green or max iterations (10) reached

## Examples

```
/ci-triage                          # Single pass on current branch's PR
/ci-triage 7631                     # Single pass on PR #7631
/ci-triage 7631 watch until green   # Watch mode until all pass
/ci-triage run until all pass       # Watch mode on current PR
```

$ARGUMENTS
