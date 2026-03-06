---
description: Backport a merged HyperShift PR to one or more release branches, handling Jira integration and cherry-pick conflicts
---

Backport a merged PR to one or more release branches in the HyperShift repository.

This command uses the **hypershift-backport** skill for naming conventions, Jira integration, and cherry-pick guidelines. Always apply those conventions throughout this process.

**HyperShift Backport PR to Release Branches**

## Usage Examples:

1. **Backport all commits to a single release branch**:
   `/hypershift-backport 7730 release-4.21`

2. **Backport to multiple release branches**:
   `/hypershift-backport 7730 release-4.21,release-4.20`

3. **Backport specific commits only**:
   `/hypershift-backport 7730 release-4.18 abc1234,def5678`

4. **Backport using PR URL**:
   `/hypershift-backport https://github.com/openshift/hypershift/pull/7730 release-4.21`

## Arguments:

- {{args.0}}: PR number or full GitHub URL (required)
  - Examples: `7730`, `https://github.com/openshift/hypershift/pull/7730`
- {{args.1}}: Comma-separated list of target release branches (required)
  - Examples: `release-4.21`, `release-4.21,release-4.20`
- {{args.2}}: Comma-separated list of commit SHAs to cherry-pick (optional)
  - If omitted, all commits from the PR are cherry-picked
  - Examples: `abc1234`, `abc1234,def5678`

## Process Flow:

### Step 1: Parse Input and Validate PR

Extract the PR number from the argument:

```bash
PR_NUMBER=$(echo "{{args.0}}" | grep -oE '[0-9]+$')
TARGET_BRANCHES="{{args.1}}"
SPECIFIC_COMMITS="{{args.2}}"  # optional, comma-separated SHAs
```

Fetch PR details and validate:

```bash
gh pr view "$PR_NUMBER" --json number,title,author,headRefName,baseRefName,body,state,url,commits,mergeCommit
```

**Required validations:**
- PR must exist
- PR state must be `MERGED`
- Working directory must be clean (no uncommitted changes)
- Each target branch must exist in the remote (verify with `git ls-remote`)

**Determine the source branch:** Read the `baseRefName` from the PR. This is the branch the PR was merged into (usually `main`, but could be another `release-4.x`).

**Error examples:**
```
ERROR: PR #7730 is not merged (current state: OPEN).
Backports can only be created from merged PRs.
```
```
ERROR: Branch 'release-4.99' does not exist in remote.
```

### Step 2: Post Backport Comment on Source PR

Post the `/jira backport` comment to trigger Prow CI:

```bash
gh pr comment "$PR_NUMBER" --body "/jira backport $TARGET_BRANCHES"
```

Report to the user that the comment has been posted.

### Step 3: Wait for Prow CI Response with Jira IDs

Poll the PR comments to find Prow bot's response containing OCPBUGS issue IDs.

```bash
# Poll comments every 30 seconds, up to 5 minutes
gh pr view "$PR_NUMBER" --json comments --jq '.comments[] | select(.author.login | test("openshift-ci|prow|bot")) | .body'
```

**What to look for:**
- Jira issue IDs in format `OCPBUGS-XXXXX`
- Each target branch should get its own Jira issue

**Extract and store:** Map each target branch to its corresponding OCPBUGS-XXXXX ID. Report each Jira ID to the user as found.

**Timeout handling (5 minutes):** Ask the user if they want to:
1. Continue waiting
2. Provide the Jira IDs manually
3. Abort

### Step 4: Wait for Automatic Cherry-Pick PRs

After Jira IDs are found, Prow attempts automatic cherry-picks. Poll for results:

```bash
# Poll every 30 seconds, up to 5 minutes
gh pr view "$PR_NUMBER" --json comments --jq '.comments[] | select(.body | test("cherry-pick|backport|conflict")) | .body'
```

**For each target branch, detect:**

1. **Auto cherry-pick succeeded**: New PR link in comments. Report to user. This branch is done.
2. **Auto cherry-pick failed**: Conflict/failure mentioned in comments. Proceed to Step 5.

### Step 5: Manual Cherry-Pick (for failed auto cherry-picks only)

Process each failed target branch independently.

#### 5.1: Load environment and identify remotes

```bash
[ -f dev/claude-env.sh ] && source dev/claude-env.sh
```

**Auto-detect remotes from `git remote -v`:**
- The remote pointing to `openshift/hypershift` is the upstream remote
- The remote pointing to the user's fork (any other) is the fork remote
- Fallback: `GIT_UPSTREAM_REMOTE="${GIT_UPSTREAM_REMOTE:-upstream}"`, `GIT_FORK_REMOTE="${GIT_FORK_REMOTE:-origin}"`

#### 5.2: Save current state and checkout target branch

```bash
ORIGINAL_BRANCH=$(git rev-parse --abbrev-ref HEAD)

git fetch "$GIT_UPSTREAM_REMOTE"
git checkout "$TARGET_BRANCH"
git pull --rebase "$GIT_UPSTREAM_REMOTE" "$TARGET_BRANCH"
```

#### 5.3: Create backport branch

Following the hypershift-backport skill conventions:

```bash
# Extract short version (release-4.21 -> 421)
VERSION_SHORT=$(echo "$TARGET_BRANCH" | sed 's/release-//' | tr -d '.')

# Create backport branch
git checkout -b "bp${VERSION_SHORT}/${JIRA_ID}"
```

#### 5.4: Determine and confirm commits to cherry-pick

If specific commits were provided via `{{args.2}}`, use those. Otherwise, get all commits from the PR.

```bash
if [ -n "$SPECIFIC_COMMITS" ]; then
  COMMITS=$(echo "$SPECIFIC_COMMITS" | tr ',' ' ')
else
  COMMITS=$(gh pr view "$PR_NUMBER" --json commits --jq '.commits[].oid')
fi
```

**Before cherry-picking, display the commit list to the user and ask for confirmation:**
- Show each commit SHA and title
- Show the diff summary (`git show --stat <SHA>`) for each commit so the user can verify the scope
- Ask the user to confirm these are the correct commits to backport
- Only proceed after user confirmation

**CRITICAL - Scope restriction:** The backport MUST only include changes from the original PR commits. Do NOT introduce any additional changes beyond what is strictly necessary to resolve cherry-pick conflicts. Any modification not present in the original PR diff is out of scope.

Cherry-pick them **in chronological order**:

```bash
for COMMIT in $COMMITS; do
  git cherry-pick "$COMMIT"
done
```

#### 5.5: Resolve conflicts

When cherry-pick conflicts occur:
1. Identify conflicting files: `git status`
2. Read conflicting files and understand the conflict markers
3. Resolve conflicts preserving the intent of the original change while adapting to the target branch
4. Stage resolved files:
   ```bash
   git add <resolved-files>
   git cherry-pick --continue
   ```
5. Repeat for each commit with conflicts

**CRITICAL - Do NOT introduce unrelated changes during conflict resolution:**
- Only resolve the conflict markers. Do NOT modify, refactor, or "fix" any code outside the conflict regions.
- Only stage files that were part of the original PR. If `git status` shows modified files that were NOT in the original PR diff, discard those changes with `git checkout -- <file>`.
- After resolving, run `git diff HEAD~1 --stat` and compare against the original PR's changed files. If any file appears that was not in the original PR, investigate and remove that change before continuing.

**Always ask the user for confirmation before proceeding if conflicts are complex or ambiguous.**

#### 5.6: Verify backport scope

Before building, verify that the backport only contains changes from the original PR.

```bash
# Get the list of files changed in the original PR
ORIGINAL_FILES=$(gh pr view "$PR_NUMBER" --json files --jq '.files[].path' | sort)

# Get the list of files changed in the backport (compared to the target branch)
BACKPORT_FILES=$(git diff "$GIT_UPSTREAM_REMOTE/$TARGET_BRANCH" --name-only | sort)

# Identify any files in the backport that were NOT in the original PR
EXTRA_FILES=$(comm -13 <(echo "$ORIGINAL_FILES") <(echo "$BACKPORT_FILES"))
```

**If extra files are found:**
- Display the list of extra files to the user
- These are changes NOT present in the original PR and must be removed
- Revert those files: `git checkout "$GIT_UPSTREAM_REMOTE/$TARGET_BRANCH" -- <extra-file>`
- Amend the commit: `git commit --amend -s --no-edit`
- Ask the user for confirmation before continuing

#### 5.7: Build, test, and verify

All three checks are **mandatory** before pushing. Run them sequentially:

```bash
make build
make test
make verify
```

**If any fails:**
- Investigate and fix issues caused by the backport
- Only fix issues directly related to the cherry-picked changes. Do NOT fix pre-existing issues in the target branch
- Stage fixes and amend the cherry-pick commit (`git commit --amend -s`)
- Re-run all three checks from the beginning
- If unable to fix, report failure and leave the branch for manual investigation

#### 5.8: Push and create PR

```bash
git push -u "$GIT_FORK_REMOTE" "bp${VERSION_SHORT}/${JIRA_ID}"

gh pr create \
  --title "[${TARGET_BRANCH}] ${JIRA_ID}: ${ORIGINAL_PR_TITLE}" \
  --base "$TARGET_BRANCH" \
  --body "## Backport

This is a manual backport of #${PR_NUMBER} to \`${TARGET_BRANCH}\`.

The automatic cherry-pick failed due to conflicts which have been resolved manually.

### Original PR
- **PR:** #${PR_NUMBER}
- **Title:** ${ORIGINAL_PR_TITLE}
- **Author:** ${ORIGINAL_AUTHOR}

### Jira
- **Issue:** [${JIRA_ID}](https://issues.redhat.com/browse/${JIRA_ID})

---

🤖 Generated with \`/hypershift-backport\` command via [Claude Code](https://claude.com/claude-code)"
```

### Step 6: Return to original state

```bash
git checkout "$ORIGINAL_BRANCH"
```

## Expected Output Format:

### All auto cherry-picks succeeded:
```
============================================
BACKPORT COMPLETE
============================================

Source PR: #7730 (main)
Backport comment posted: /jira backport release-4.21,release-4.20

Jira Issues Created:
  - release-4.21: OCPBUGS-12345
  - release-4.20: OCPBUGS-12346

Cherry-Pick PRs (auto-generated by Prow):
  - release-4.21: https://github.com/openshift/hypershift/pull/7800
  - release-4.20: https://github.com/openshift/hypershift/pull/7801

Next steps:
  1. Review the backport PRs
  2. Ensure CI passes
  3. Get approvals and merge
```

### Some manual cherry-picks needed:
```
============================================
BACKPORT COMPLETE
============================================

Source PR: #7730 (main)

Results:
  - release-4.21: Auto cherry-pick succeeded -> PR #7800
  - release-4.20: Manual backport created -> PR #7802
    - Jira: OCPBUGS-12346
    - Branch: bp420/OCPBUGS-12346
    - Conflicts resolved: 3 files
    - make build: PASSED
    - make test: PASSED
    - make verify: PASSED

Next steps:
  1. Review the backport PRs
  2. Ensure CI passes
  3. Get approvals and merge
```

### Failure:
```
============================================
BACKPORT FAILED
============================================

Source PR: #7730 (main)
Target branch: release-4.20
Jira: OCPBUGS-12346

Failure reason: make test/verify failed after conflict resolution.

The backport branch 'bp420/OCPBUGS-12346' is available locally
for manual investigation.

Returning to original branch: main
```

## Error Handling:

| Scenario | Action |
|----------|--------|
| PR not found | `ERROR: PR #99999 not found or you don't have access.` |
| PR not merged | `ERROR: PR #7730 is not merged (current state: OPEN). Backports require merged PRs.` |
| Target branch doesn't exist | `ERROR: Branch 'release-4.99' does not exist in remote.` |
| Dirty working directory | `ERROR: Working directory has uncommitted changes. Please commit or stash before running.` |
| Prow timeout (Jira IDs) | Ask user: wait longer, provide IDs manually, or abort |
| Prow timeout (cherry-pick) | Proceed with manual cherry-pick |
| Cherry-pick conflicts | Resolve automatically, ask user if ambiguous, verify with build+test+verify |
| make build fails | Report failure, leave branch for manual investigation |
| make test fails | Report failure, leave branch for manual investigation |
| make verify fails | Report failure, leave branch for manual investigation |
| git push fails | `ERROR: Failed to push branch. Check your permissions and credentials.` |
| PR creation fails | `ERROR: Failed to create PR. Branch has been pushed, create PR manually.` |

## Safety Features:

- **Merge verification**: Only processes merged PRs
- **Clean state required**: Refuses to run if working directory has uncommitted changes
- **Scope verification**: Compares backport diff against original PR files to detect and remove unrelated changes
- **Full verification**: Runs `make build`, `make test`, and `make verify` before creating PR
- **Branch restoration**: Always returns to original branch after completion
- **Non-destructive**: Never force-pushes or modifies the source PR
- **Independent processing**: Each target branch is handled independently; failure in one does not block others
- **User confirmation**: Asks for confirmation on ambiguous conflict resolutions
- **Commit signing**: Uses `git commit -s` for all commits per project conventions

## Requirements:

- `gh` CLI installed and authenticated with push/PR access
- `git` configured with `user.name` and `user.email`
- `make` and Go toolchain available
- Clean working directory (no uncommitted changes)
- Access to the upstream remote for fetching release branches
