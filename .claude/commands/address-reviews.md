---
description: Fetch and address all PR review comments
argument-hint: [PR number (optional - uses current branch if omitted)]
---

You are tasked with addressing all review comments on a GitHub Pull Request.

## Step 0: Checkout the PR Branch

1. **Determine PR number**: Use $ARGUMENTS if provided, otherwise `gh pr list --head <current-branch>`
2. **Checkout**: Use `gh pr checkout <PR_NUMBER>` if not already on the branch, then `git pull`
3. **Verify clean working tree**: Run `git status`. If uncommitted changes exist, ask user how to proceed

## Step 1: Fetch PR Context

1. **Fetch PR metadata with selective filtering**:

   a. **First pass - Get metadata only** (IDs, authors, lengths, URLs):
   ```bash
   # Get issue comments (general PR comments - main conversation)
   gh pr view <PR_NUMBER> --json comments --jq '.comments | map({
     id,
     author: .author.login,
     length: (.body | length),
     url,
     createdAt,
     type: "issue_comment"
   })'

   # Get reviews (need REST API for numeric IDs)
   gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/reviews --jq 'map({
     id,
     author: .user.login,
     length: (.body | length),
     state,
     submitted_at,
     type: "review"
   })'

   # Get review comments (inline code comments)
   gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments --jq 'map({
     id,
     author: .user.login,
     length: (.body | length),
     path,
     line,
     created_at,
     type: "review_comment"
   })'
   ```

   b. **Apply filtering logic** (DO NOT fetch full body yet):
   - Filter out: `line == null` (outdated review comments)
   - Filter out: `length > 5000`
   - Filter out: CI/automation bots `author in ["openshift-ci-robot", "openshift-ci"]` (keep coderabbitai for code review insights)
   - Keep track of filtered items and stats for reporting

   c. **Second pass - Fetch ONLY essential fields for kept items**:
   ```bash
   # For issue comments - fetch only body and minimal metadata:
   gh api repos/{owner}/{repo}/issues/comments/<comment_id> --jq '{id, body, user: .user.login, created_at, url}'

   # For reviews - fetch only body and state:
   gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/reviews/<review_id> --jq '{id, body, user: .user.login, state, submitted_at}'

   # For review comments - fetch only body and code context:
   gh api repos/{owner}/{repo}/pulls/comments/<comment_id> --jq '{id, body, user: .user.login, path, position, diff_hunk, created_at}'
   ```

   **Note**: Using `--jq` to select only needed fields minimizes context usage. Avoid fetching full API responses with all metadata.

   d. **Log filtering results**:
   ```
   ℹ️  Fetched N/M comments (filtered out K large/bot comments saving ~X chars)
   ```

2. **Fetch commit messages**: `gh pr view <PR_NUMBER> --json commits -q '.commits[] | "\(.messageHeadline)\n\n\(.messageBody)"'`

3. Store ONLY the kept (filtered) comments for analysis

## Step 2: Categorize and Prioritize Comments

**Note**: Most filtering already happened in Step 1 to save context window space.

1. **Additional filtering** (for remaining fetched comments):
   - Already resolved comments
   - Pure acknowledgments ("LGTM", "Thanks!", etc.)

2. **Categorize**:
   - **BLOCKING**: Critical changes (security, bugs, breaking issues)
   - **CHANGE_REQUEST**: Code improvements or refactoring
   - **QUESTION**: Requests for clarification
   - **SUGGESTION**: Optional improvements (nits, non-critical)

3. **Group by context**: Group by file, then by proximity (within 10 lines)

4. **Prioritize**: BLOCKING → CHANGE_REQUEST → QUESTION → SUGGESTION

5. **Present summary**: Show counts by category and file groupings, ask user to confirm

## Step 3: Address Comments

### Grouped Comments

When multiple comments relate to the same concern/fix:
- Make the code change once
- Reply to EACH comment individually (don't copy-paste, tailor each reply)
- Optional reference: `Done. (Also addresses feedback from @user)`

### Code Change Requests

**a. Validate**: Thoroughly analyze if the change is valid and fixes an issue or improves code. Don't be afraid to reject the change if it doesn't make sense.

**b. If requested change is valid**:
- Plan and implement changes
- Commit and Push
   1. **Review changes**: `git diff`

   2. **Analyze commit structure**: `git log --oneline origin/main..HEAD`
      - Identify which commit the changes relate to

   3. **Commit strategy**:

      **DEFAULT: Amend the relevant commit**

      - ✅ **AMEND**: Review fixes, bug fixes, style improvements, refactoring, docs, tests within PR scope
      - ❌ **NEW COMMIT**: Only for substantial new features beyond PR's original scope
      - **When unsure**: Amend (keep git history clean)
      - **Multiple commits**: Use `git rebase -i origin/main` to amend the specific relevant commit

   4. **Create and push commit**:
      - Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format
      - Always include body explaining "why"
      - **Amend**: `git commit --amend --no-edit && git push --force-with-lease` (or update message if scope changed)
      - **New commit**: Standard commit with message

- **Concise Reply template**: `Done. [1-line what changed]. [Optional 1-line why]`
  - Max 2 sentences + attribution footer
- Post reply:
  ```
  gh api repos/{owner}/{repo}/pulls/<PR_NUMBER>/comments/<comment_id>/replies -f body="<reply>"
  ```
  If fails: `gh pr comment <PR_NUMBER> --body="@<author> <reply>"`

**c. If declining change**:
- **Reply with technical explanation** (3-5 sentences):
  - Why current implementation is correct
  - Specific reasoning with file:line references
- Use same posting method as (b)

**d. If unsure**: Ask user for clarification

### Clarification Requests

- Provide clear, detailed answer (2-4 sentences)
- Include file:line references when applicable
- Post using same method as code changes

### Informational Comments

- No action unless response is courteous

**All replies must include**: `---\n*AI-assisted response via Claude Code*`

## Step 4: Summary

Show user:
- Total comments found (raw count from API)
- Comments filtered out (with reason: outdated/large/bot-generated)
- Comments addressed with code changes
- Comments replied to
- Comments requiring user input

## Guidelines

- Be thorough but efficient
- Maintain professional tone in all replies
- Prioritize code quality over quick fixes
- Ensure code builds and passes tests after changes
- When in doubt, ask the user
- Use TodoWrite to track progress through multiple comments
