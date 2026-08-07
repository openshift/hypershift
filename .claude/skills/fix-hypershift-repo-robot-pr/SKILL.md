---
name: fix-hypershift-repo-robot-pr
description: >
  Fix robot/bot-authored PRs in the HyperShift repo that have failing CI due to missing
  generated files. Use when a Dependabot, Konflux, Renovate, or other bot PR fails
  verification and needs regenerated files, vendoring, and a clean replacement PR with
  conventional commit formatting.
---

# Fix HyperShift Repo Robot PR

Validate a bot-authored PR, cherry-pick its commits, regenerate missing files, organize
changes into logical commits, and create a clean replacement PR.

## Usage

```
/skill:fix-hypershift-repo-robot-pr <pr-number-or-url>
```

**Arguments:**
- `pr-number-or-url` (required): GitHub PR number or full URL
  - Examples: `7435`, `https://github.com/openshift/hypershift/pull/7435`

## What This Skill Does

1. Validates the PR is authored by a bot (`is_bot: true`)
2. Fetches the bot's PR commits
3. Creates a new branch `fix/<original-branch-name>` from the base branch
4. Cherry-picks commits and converts to conventional commit format
5. Runs `make verify` to regenerate all necessary files
6. Runs `UPDATE=true make test` to update test fixtures
7. Organizes changes into logical, well-structured commits
8. Runs `make verify` and `make test` for final validation
9. If successful: creates new PR and closes/comments on original
10. If unsuccessful: preserves original PR and reports failure

## Process Flow

### Step 1: Parse Input and Validate PR

Extract the PR number:

```bash
PR_NUMBER=$(echo "<user-argument>" | grep -oE '[0-9]+$')
```

Fetch PR details and validate:

```bash
gh pr view "$PR_NUMBER" --json number,title,author,headRefName,baseRefName,body,state,url,commits
```

**Required validations:**
- PR must exist and be `OPEN`
- `author.is_bot` must be `true`
- Working directory must be clean (no uncommitted changes)

**Error if not a bot:**
```
ERROR: PR #7435 is not authored by a bot.
Author: username (is_bot: false)

This skill is specifically for fixing bot-authored PRs.
```

### Step 2: Create Fix Branch from Base

```bash
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)
git fetch upstream
FIX_BRANCH="fix/${BOT_BRANCH_NAME}"
git checkout -b "$FIX_BRANCH" upstream/${BASE_REF_NAME}
```

### Step 3: Cherry-pick and Convert Commits

For each commit in the bot's PR:
1. Cherry-pick the commit
2. Amend the commit message to conventional commit format

**Commit message conversion rules:**

| Bot | Original Format | Converted Format |
|-----|-----------------|------------------|
| Dependabot | `NO-JIRA: Bump the misc-dependencies group...` | `chore(deps): bump misc-dependencies group...` |
| Dependabot | `build(deps): bump X from A to B` | Keep as-is (already conventional) |
| Konflux | `Red Hat Konflux update hypershift-operator...` | `chore(konflux): update hypershift-operator...` |

```bash
for COMMIT in $(gh pr view $PR_NUMBER --json commits -q '.commits[].oid'); do
  git cherry-pick "$COMMIT"
  ORIGINAL_MSG=$(git log -1 --format='%B')
  # Convert message per rules above
  git commit --amend -m "$CONVERTED_MSG"
done
```

### Step 4: Run make verify, Update Test Fixtures, and Organize Changes

```bash
make verify 2>&1 || true
UPDATE=true make test 2>&1 || true
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

Check for untracked generated files:
```bash
git status --porcelain | grep '^??'
```

### Step 5: Run Full Validation

```bash
make verify
make test
```

### Step 6: Handle Results

**If validation fails — ABORT and preserve original:**

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

**If validation succeeds — Create new PR and close/comment original:**

```bash
git push -u origin "$FIX_BRANCH"

# PR title must be prefixed with NO-JIRA: for bot dependency updates
gh pr create \
  --title "NO-JIRA: $CONVENTIONAL_TITLE" \
  --base "$BASE_REF_NAME" \
  --body "## Summary
This PR supersedes #${PR_NUMBER} (authored by ${AUTHOR_LOGIN}).
..."

# Try to close original, fall back to comment
gh pr close "$PR_NUMBER" --comment "..." || \
gh pr comment "$PR_NUMBER" --body "This PR has been superseded by #${NEW_PR_NUMBER}. ..."
```

## Commit Organization Strategy

| Commit | Files | Message Format |
|--------|-------|----------------|
| 1 | `go.mod`, `go.sum` | `chore(deps): update go.mod dependencies` |
| 2 | `vendor/` | `chore(deps): update vendored dependencies` |
| 3 | `api/go.mod`, `api/go.sum`, `api/vendor/` | `chore(api): update api module dependencies` |
| 4 | `cmd/install/assets/**/*.yaml` | `chore: regenerate CRD manifests` |
| 5 | Other changes | `chore: additional changes from make verify` |

## Error Handling

| Scenario | Action |
|----------|--------|
| PR not found | `ERROR: PR #99999 not found or you don't have access.` |
| PR not open | `ERROR: PR #7435 is not open (current state: MERGED).` |
| Not a bot | `ERROR: PR #7435 is not authored by a bot.` |
| Dirty working dir | `ERROR: Working directory has uncommitted changes.` |
| make verify failed | Preserve original PR, report failure |
| make test failed | Preserve original PR, report failure |
| git push failed | `ERROR: Failed to push branch. Check permissions.` |
| PR creation failed | `ERROR: Failed to create new PR. Fix branch pushed, create PR manually.` |
| No close permission | Falls back to commenting on original PR |

## Safety Features

- **Bot verification**: Only processes PRs with `is_bot: true`
- **Atomic operations**: Original PR only closed AFTER new PR is created
- **Failure preservation**: Original PR is NEVER closed if validation fails
- **Clean state required**: Refuses to run with uncommitted changes
- **Branch restoration**: Always returns to original branch after completion
- **Local cleanup**: Deletes fix branch locally on failure
- **Permission fallback**: Comments on original PR if no permission to close

## Supported Bots

| Bot | Author Login | Typical PRs |
|-----|-------------|-------------|
| Dependabot | `app/dependabot` | Go dependency updates |
| Konflux | `app/red-hat-konflux` | Image and pipeline updates |
| Renovate | `app/renovate` | Dependency updates |
| Any | `is_bot: true` | Various automated updates |

## Requirements

- `gh` CLI installed and authenticated with push/PR access
- `git` configured with `user.name` and `user.email`
- `make` and Go toolchain available
- Clean working directory (no uncommitted changes)
