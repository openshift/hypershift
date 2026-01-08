---
description: Fix robot/bot PRs in HyperShift repo by regenerating files and creating a new PR with passing verification
---

Fix robot/bot-authored PRs in the HyperShift repository that have failing CI due to missing generated files.

[Extended thinking: This command validates a PR is from a bot, checks out the bot's branch, creates a fix branch, runs make verify to regenerate files, commits changes, runs validation, and if successful creates a new PR while closing the original. If validation fails, the original PR is preserved.]

**Fix HyperShift Repo Robot PR**

## Usage Examples:

1. **Fix a dependabot PR by number**:
   `/fix-hypershift-repo-robot-pr 7435`

2. **Fix a konflux PR by URL**:
   `/fix-hypershift-repo-robot-pr https://github.com/openshift/hypershift/pull/7332`

## What This Command Does:

1. Validates the PR is authored by a bot (`is_bot: true`)
2. Checks out the bot's PR branch
3. Creates a new branch: `fix/<original-branch-name>`
4. Runs `make verify` to regenerate all necessary files
5. Commits any changes with proper conventional commit format
6. Runs `make verify` and `make test` for final validation
7. If successful: Creates new PR and closes original with reference
8. If unsuccessful: Preserves original PR and reports failure

## Process Flow:

### Step 1: Parse Input and Validate PR

Extract the PR number from the argument (handles both number and URL formats):

```bash
PR_NUMBER=$(echo "{{args.0}}" | grep -oE '[0-9]+$')
```

Fetch PR details and validate:

```bash
gh pr view "$PR_NUMBER" --json number,title,author,headRefName,baseRefName,body,state,url
```

**Required validations:**
- PR must exist
- PR state must be `OPEN`
- `author.is_bot` must be `true`
- Working directory must be clean (no uncommitted changes)

**Error if not a bot:**
```
ERROR: PR #7435 is not authored by a bot.
Author: username (is_bot: false)

This command is specifically for fixing bot-authored PRs.
```

### Step 2: Checkout PR and Create Fix Branch

```bash
# Save current branch
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Checkout the bot's PR
gh pr checkout "$PR_NUMBER"

# Get bot's branch name
BOT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Create fix branch
FIX_BRANCH="fix/${BOT_BRANCH}"
git checkout -b "$FIX_BRANCH"
```

**Branch name examples:**
- `dependabot/go_modules/github-dependencies-0a2d1f925e` → `fix/dependabot/go_modules/github-dependencies-0a2d1f925e`
- `konflux-hypershift-operator-hotfix-ocpbugs-61296-0170` → `fix/konflux-hypershift-operator-hotfix-ocpbugs-61296-0170`

### Step 3: Run make verify and Commit Changes

```bash
# Run make verify (may generate files)
make verify 2>&1 || true

# Check for changes and commit if any
if ! git diff --quiet || ! git diff --cached --quiet; then
  git add -A
  git commit -m "chore: regenerate files after bot PR changes

Regenerated files using \`make verify\` to fix CI failures
on bot-authored PR #${PR_NUMBER}.

Signed-off-by: $(git config user.name) <$(git config user.email)>
Commit-Message-Assisted-by: Claude (via Claude Code)"
fi
```

### Step 4: Run Full Validation

```bash
# Run make verify (should pass now with clean git state)
make verify

# Run make test
make test
```

### Step 5: Handle Results

**If validation fails - ABORT and preserve original:**

```
============================================
VALIDATION FAILED - ABORTING
============================================

The original PR #7435 has been PRESERVED.
Please investigate the failures manually.

- make verify: FAILED/PASSED
- make test: FAILED/PASSED

Returning to original branch...
```

- Clean up fix branch locally
- Return to original branch
- DO NOT close original PR

**If validation succeeds - Create new PR and close original:**

```bash
# Push fix branch
git push -u origin "$FIX_BRANCH"

# Create new PR with reference to original
gh pr create \
  --title "$ORIGINAL_TITLE" \
  --base "$BASE_REF_NAME" \
  --body "## Summary

This PR supersedes #${PR_NUMBER} (authored by ${AUTHOR_LOGIN}).

The original bot PR required regeneration of files to pass CI verification. This PR includes:
- All changes from the original bot PR
- Regenerated files from \`make verify\`

### Original PR Description

${ORIGINAL_BODY}

---

**Original PR:** #${PR_NUMBER}
**Bot Author:** ${AUTHOR_LOGIN}

---

Assisted-by: Claude (via Claude Code)"

# Close original PR with reference
gh pr close "$PR_NUMBER" --comment "This PR has been superseded by #${NEW_PR_NUMBER}.

The new PR includes all changes from this bot PR plus regenerated files from \`make verify\` to pass CI verification.

See: ${NEW_PR_URL}

---
*Automated by Claude Code /fix-hypershift-repo-robot-pr command*"
```

## Expected Output Format:

### Success:
```markdown
============================================
SUCCESS
============================================

Original PR #7435 has been closed.
New PR created: https://github.com/openshift/hypershift/pull/7500

The new PR includes:
  - All changes from the bot PR
  - Regenerated files from make verify

Next steps:
  1. Review the new PR: https://github.com/openshift/hypershift/pull/7500
  2. Request reviews as needed
  3. Merge when CI passes
```

### Failure:
```markdown
============================================
VALIDATION FAILED - ABORTING
============================================

The original PR #7435 has been PRESERVED.
Please investigate the failures manually.

- make verify: FAILED
- make test: PASSED

The fix branch 'fix/dependabot/go_modules/...' has been deleted locally.
```

## Error Handling:

| Scenario | Message |
|----------|---------|
| PR not found | `ERROR: PR #99999 not found or you don't have access.` |
| PR not open | `ERROR: PR #7435 is not open (current state: MERGED).` |
| Not a bot | `ERROR: PR #7435 is not authored by a bot. Author: username (is_bot: false)` |
| Dirty working directory | `ERROR: Working directory has uncommitted changes. Please commit or stash before running.` |
| make verify failed | `ERROR: make verify failed. Original PR #7435 has been PRESERVED.` |
| make test failed | `ERROR: make test failed. Original PR #7435 has been PRESERVED.` |
| git push failed | `ERROR: Failed to push branch. Check your permissions and credentials.` |
| PR creation failed | `ERROR: Failed to create new PR. The fix branch has been pushed but you may need to create the PR manually.` |

## Safety Features:

- **Bot verification**: Only processes PRs with `is_bot: true` in author data
- **Atomic operations**: Original PR only closed AFTER new PR is successfully created
- **Failure preservation**: Original PR is NEVER closed if validation fails
- **Clean state required**: Refuses to run if working directory has uncommitted changes
- **Branch restoration**: Always returns to original branch after completion
- **Local cleanup**: Deletes fix branch locally on failure to avoid clutter

## Commit Message Format:

```
chore: regenerate files after bot PR changes

Regenerated files using `make verify` to fix CI failures
on bot-authored PR #7435.

Signed-off-by: Bryan Cox <brcox@redhat.com>
Commit-Message-Assisted-by: Claude (via Claude Code)
```

## Supported Bots:

Any bot author is supported. Common bots in HyperShift:

| Bot | Author Login | Typical PRs |
|-----|-------------|-------------|
| Dependabot | `app/dependabot` | Go dependency updates |
| Konflux | `app/red-hat-konflux` | Image and pipeline updates |
| Renovate | `app/renovate` | Dependency updates |
| Any | `is_bot: true` | Various automated updates |

## Requirements:

- `gh` CLI must be installed and authenticated with push/PR access
- `git` must be configured with `user.name` and `user.email`
- `make` and Go toolchain must be available
- Clean working directory (no uncommitted changes)

## Arguments:

- {{args.0}}: PR number or full GitHub URL (required)
  - Examples: `7435`, `https://github.com/openshift/hypershift/pull/7435`

The command will provide progress updates through the TodoWrite tool and report success or failure with detailed information.
