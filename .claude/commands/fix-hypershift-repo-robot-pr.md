---
description: Fix robot/bot PRs in HyperShift repo by regenerating files and creating a new PR with passing verification
---

Fix robot/bot-authored PRs in the HyperShift repository that have failing CI due to missing generated files.

[Extended thinking: This command validates a PR is from a bot, checks out the bot's branch, creates a fix branch, cherry-picks commits with conventional commit format, runs make verify to regenerate files, organizes changes into logical commits, runs validation, and if successful creates a new PR while closing/commenting on the original. If validation fails, the original PR is preserved.]

**Fix HyperShift Repo Robot PR**

## Usage Examples:

1. **Fix a dependabot PR by number**:
   `/fix-hypershift-repo-robot-pr 7435`

2. **Fix a konflux PR by URL**:
   `/fix-hypershift-repo-robot-pr https://github.com/openshift/hypershift/pull/7332`

## What This Command Does:

1. Validates the PR is authored by a bot (`is_bot: true`)
2. Fetches the bot's PR commits
3. Creates a new branch: `fix/<original-branch-name>` from the base branch
4. Cherry-picks commits and converts to conventional commit format
5. Runs `make verify` to regenerate all necessary files
6. Runs `UPDATE=true make test` to update test fixtures
7. Organizes changes into logical, well-structured commits
8. Runs `make verify` and `make test` for final validation
9. If successful: Creates new PR and closes/comments on original
10. If unsuccessful: Preserves original PR and reports failure

## Process Flow:

### Step 1: Parse Input and Validate PR

Extract the PR number from the argument (handles both number and URL formats):

```bash
PR_NUMBER=$(echo "{{args.0}}" | grep -oE '[0-9]+$')
```

Fetch PR details and validate:

```bash
gh pr view "$PR_NUMBER" --json number,title,author,headRefName,baseRefName,body,state,url,commits
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

### Step 2: Create Fix Branch from Base

```bash
# Save current branch
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Fetch latest from upstream
git fetch upstream

# Create fix branch from the PR's base branch
FIX_BRANCH="fix/${BOT_BRANCH_NAME}"
git checkout -b "$FIX_BRANCH" upstream/${BASE_REF_NAME}
```

**Branch name examples:**
- `dependabot/go_modules/github-dependencies-0a2d1f925e` → `fix/dependabot/go_modules/github-dependencies-0a2d1f925e`
- `konflux-hypershift-operator-hotfix-ocpbugs-61296-0170` → `fix/konflux-hypershift-operator-hotfix-ocpbugs-61296-0170`

### Step 3: Cherry-pick and Convert Commits

For each commit in the bot's PR:
1. Cherry-pick the commit
2. Amend the commit message to conventional commit format

**Converting bot commit messages to conventional format:**

| Bot | Original Format | Converted Format |
|-----|-----------------|------------------|
| Dependabot | `NO-JIRA: Bump the misc-dependencies group...` | `chore(deps): bump misc-dependencies group...` |
| Dependabot | `build(deps): bump X from A to B` | Keep as-is (already conventional) |
| Konflux | `Red Hat Konflux update hypershift-operator...` | `chore(konflux): update hypershift-operator...` |

**Note:** The conversion logic below is pseudocode. Claude should implement the conversion based on the rules in the table above, detecting the bot type from the commit message format and transforming accordingly.

```bash
# Cherry-pick each commit from the bot PR
for COMMIT in $(gh pr view $PR_NUMBER --json commits -q '.commits[].oid'); do
  git cherry-pick "$COMMIT"

  # Get original message and convert to conventional format
  # Claude implements conversion based on the table above:
  # - Strip "NO-JIRA: " prefix if present
  # - Convert "Bump" to "bump" for consistency
  # - Add appropriate type prefix (chore(deps):, chore(konflux):, etc.)
  # - Preserve the rest of the message
  ORIGINAL_MSG=$(git log -1 --format='%B')

  # Amend with converted message
  git commit --amend -m "$CONVERTED_MSG"
done
```

### Step 4: Run make verify, Update Test Fixtures, and Organize Changes

Run make verify to regenerate files, run tests with UPDATE=true to update fixtures, then organize into logical commits:

```bash
# Run make verify (may generate files)
make verify 2>&1 || true

# Run make test with UPDATE=true to update test fixtures
UPDATE=true make test 2>&1 || true

# Check for changes
git status --porcelain
```

**Organize changes into separate commits:**

1. **go.mod/go.sum changes** (root module):
   ```bash
   git add go.mod go.sum
   git commit -m "chore(deps): update go.mod dependencies

   Signed-off-by: ...
   Commit-Message-Assisted-by: Claude (via Claude Code)"
   ```

2. **vendor/ changes** (root module):
   ```bash
   git add vendor/
   git commit -m "chore(deps): update vendored dependencies

   Signed-off-by: ...
   Commit-Message-Assisted-by: Claude (via Claude Code)"
   ```

3. **api/ module changes** (go.mod, go.sum, vendor/):
   ```bash
   git add api/go.mod api/go.sum api/vendor/
   git commit -m "chore(api): update api module dependencies

   Signed-off-by: ...
   Commit-Message-Assisted-by: Claude (via Claude Code)"
   ```

4. **Regenerated assets** (CRDs, manifests):
   ```bash
   git add cmd/install/assets/
   git commit -m "chore: regenerate CRD manifests

   Signed-off-by: ...
   Commit-Message-Assisted-by: Claude (via Claude Code)"
   ```

5. **Other code changes** (if any):
   ```bash
   git add -A
   git commit -m "chore: additional changes from make verify

   Signed-off-by: ...
   Commit-Message-Assisted-by: Claude (via Claude Code)"
   ```

**Important:** Check for untracked files that make verify may generate:
```bash
git status --porcelain | grep '^??'
# Add any untracked generated files
```

### Step 5: Run Full Validation

```bash
# Run make verify (should pass now with clean git state)
make verify

# Run make test
make test
```

### Step 6: Handle Results

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

**If validation succeeds - Create new PR and close/comment original:**

```bash
# Push fix branch
git push -u origin "$FIX_BRANCH"

# Create new PR with reference to original
# PR title must be prefixed with NO-JIRA: for bot dependency updates
gh pr create \
  --title "NO-JIRA: $CONVENTIONAL_TITLE" \
  --base "$BASE_REF_NAME" \
  --body "## Summary

This PR supersedes #${PR_NUMBER} (authored by ${AUTHOR_LOGIN}).

The original bot PR required regeneration of files to pass CI verification. This PR includes:
- All changes from the original bot PR (with conventional commit format)
- Regenerated files from \`make verify\`

### Commits

This PR organizes changes into logical commits:
1. Dependency updates (go.mod/go.sum)
2. Vendored dependencies
3. API module updates
4. Regenerated assets (CRDs)

### Original PR Description

${ORIGINAL_BODY}

---

**Original PR:** #${PR_NUMBER}
**Bot Author:** ${AUTHOR_LOGIN}

---

Assisted-by: Claude (via Claude Code)"

# Try to close original PR, fall back to comment if no permission
gh pr close "$PR_NUMBER" --comment "..." || \
gh pr comment "$PR_NUMBER" --body "This PR has been superseded by #${NEW_PR_NUMBER}.
A maintainer can close this PR.
..."
```

## Expected Output Format:

### Success:
```markdown
============================================
SUCCESS
============================================

Original PR #7435 has been closed (or commented).
New PR created: https://github.com/openshift/hypershift/pull/7500

The new PR includes:
  - All changes from the bot PR (conventional commits)
  - Regenerated files from make verify
  - Organized into logical commits

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

## Commit Organization Strategy:

Changes should be organized into logical commits for easier review:

| Commit | Files | Message Format |
|--------|-------|----------------|
| 1 | `go.mod`, `go.sum` | `chore(deps): update go.mod dependencies` |
| 2 | `vendor/` | `chore(deps): update vendored dependencies` |
| 3 | `api/go.mod`, `api/go.sum`, `api/vendor/` | `chore(api): update api module dependencies` |
| 4 | `cmd/install/assets/**/*.yaml` | `chore: regenerate CRD manifests` |
| 5 | Other changes | `chore: additional changes from make verify` |

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
| No close permission | Falls back to adding a comment on original PR instead of closing |

## Safety Features:

- **Bot verification**: Only processes PRs with `is_bot: true` in author data
- **Atomic operations**: Original PR only closed AFTER new PR is successfully created
- **Failure preservation**: Original PR is NEVER closed if validation fails
- **Clean state required**: Refuses to run if working directory has uncommitted changes
- **Branch restoration**: Always returns to original branch after completion
- **Local cleanup**: Deletes fix branch locally on failure to avoid clutter
- **Permission fallback**: Comments on original PR if no permission to close

## Supported Bots:

Any bot author is supported. Common bots in HyperShift:

| Bot | Author Login | Typical PRs |
|-----|-------------|-------------|
| Dependabot | `app/dependabot` | Go dependency updates |
| Konflux | `app/red-hat-konflux` | Image and pipeline updates |
| Renovate | `app/renovate` | Dependency updates |
| Any | `is_bot: true` | Various automated updates |

## PR Title Format:

The new PR title must be prefixed with `NO-JIRA:` since bot dependency updates typically don't have associated Jira tickets:

```
NO-JIRA: chore(deps): bump misc-dependencies group with 2 updates
```

## Requirements:

- `gh` CLI must be installed and authenticated with push/PR access
- `git` must be configured with `user.name` and `user.email`
- `make` and Go toolchain must be available
- Clean working directory (no uncommitted changes)

## Arguments:

- {{args.0}}: PR number or full GitHub URL (required)
  - Examples: `7435`, `https://github.com/openshift/hypershift/pull/7435`

The command will provide progress updates through the TodoWrite tool and report success or failure with detailed information.
