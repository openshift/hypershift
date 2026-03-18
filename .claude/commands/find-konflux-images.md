---
description: Find and verify Konflux-built container images from a GitHub PR
argument-hint: "<PR-URL or org/repo PR-number>"
---

# Find and verify Konflux-built container images from a PR

Given a PR, find all Konflux/Tekton pipeline-built container images, check their availability on quay.io, and report the results.

## Usage Examples

1. **By PR URL**:
   `/find-konflux-images https://github.com/openshift/hypershift/pull/7871`

2. **By repo and PR number**:
   `/find-konflux-images openshift/hypershift 7871`

## What This Command Does

1. Resolves the PR to get its head commit SHA and base branch
2. Reads the `.tekton/*-pull-request.yaml` pipeline configs from the PR's commit to extract `output-image` patterns
3. Checks which Konflux pipelines were actually triggered on the PR
4. Constructs image URLs by replacing `{{revision}}` with the commit SHA
5. Verifies each image's availability on quay.io via the quay.io tag API
6. Reports results as a Markdown table
7. Offers to retrigger expired images via `/retest` if the PR is still open

## Input

- **PR**: $ARGUMENTS (GitHub PR URL, or `org/repo` and PR number)
  - One arg: `{{args.0}}` is a full GitHub PR URL — extract org/repo and PR number from it
  - Two args: `{{args.0}}` is `org/repo`, `{{args.1}}` is PR number

## Steps

### 1. Resolve the PR

Use `gh pr view <PR> --json headRefOid,baseRefName,state` to get:
- The commit SHA (`headRefOid`)
- The base branch (`baseRefName`) — shown in the output for context
- The PR state (`state`) — used to determine if retrigger is possible

### 2. Find the pull-request pipeline templates

Look in the `.tekton/` directory at the PR's head commit for `*-pull-request.yaml` files. Read each one and extract the `output-image` value, which contains the image URL pattern with `{{revision}}` placeholder.

Use the GitHub API to fetch from the PR's commit SHA directly (not the base branch), so results are accurate even if the PR modifies `.tekton/` files:

```bash
PR_FILES=$(gh api "repos/${REPO}/contents/.tekton?ref=${COMMIT_SHA}" --jq '.[].name' | grep pull-request)

for file in $PR_FILES; do
  CONTENT=$(gh api "repos/${REPO}/contents/.tekton/${file}?ref=${COMMIT_SHA}" --jq '.content' | base64 -d)
  IMAGE_PATTERN=$(echo "$CONTENT" | grep -A1 'name: output-image' | grep 'value:' | head -1 | sed 's/.*value: *//')
  COMPONENT=$(echo "$file" | sed 's/-pull-request\.yaml$//')
done
```

### 3. Check which Konflux builds were triggered

```bash
gh pr checks ${PR_NUMBER} --repo ${REPO} | grep -i konflux
```

Only checks matching `*-on-pull-request` are image builds. The `enterprise-contract` checks are verification-only.

### 4. Verify image availability and get expiration date from quay.io

Replace `{{revision}}` in the `IMAGE_PATTERN` extracted from step 2 with the commit SHA to get the full image URL. Then parse the repo path and tag from it to query the quay.io tag API:

```bash
# Derive the full image URL from IMAGE_PATTERN
FULL_IMAGE=$(echo "$IMAGE_PATTERN" | sed "s/{{revision}}/${COMMIT_SHA}/g")
# Parse: quay.io/<REPO_PATH>:<TAG>
REPO_PATH=$(echo "$FULL_IMAGE" | sed 's|quay.io/||' | sed 's|:.*||')
TAG=$(echo "$FULL_IMAGE" | sed 's|.*:||')

# Query the quay.io tag API — returns availability, creation time, and expiration date
TAG_INFO=$(curl -s "https://quay.io/api/v1/repository/${REPO_PATH}/tag/?specificTag=${TAG}")
# Parse the response:
#   - tags[0].expiration: exact expiration date (e.g., "Sun, 22 Mar 2026 21:22:50 -0000")
#   - tags[0].start_ts: creation timestamp
#   - empty tags array means image not found (never built or already expired/garbage-collected)
```

Use `tags` array length to determine availability (non-empty = available). Extract the `expiration` field for the exact expiry date. Format it as a short date (e.g., `2026-03-22`).

### 5. Present results

Present results as a Markdown table:

| Component | Triggered | Status | Expires Date | Image URL |
|---|---|---|---|---|
| hypershift-operator-main | yes | AVAILABLE | 2026-03-22 | `quay.io/redhat-user-workloads/...` |
| control-plane-operator-main | yes | AVAILABLE | 2026-03-22 | `quay.io/redhat-user-workloads/...` |
| hypershift-gomaxprocs-webhook | no | NOT FOUND | -- | -- |

- Only show the full image URL when the status is AVAILABLE
- Show `--` in the Image URL column when the image is not found
- If a pipeline was not triggered, note that in the Status column
- Show the base branch name (e.g., `main`, `release-4.21`)

### 6. Offer to retrigger expired images

If any triggered images are NOT FOUND (likely expired), and the PR is still open, offer to retrigger by commenting `/retest` on the PR:

```bash
gh pr comment ${PR_NUMBER} --repo ${REPO} --body "/retest"
```

**Key facts about `/retest`:**
- **Prow** treats `/retest` as "rerun only failed jobs" — if all Prow jobs pass, Prow does nothing
- **Konflux (Pipelines as Code)** treats `/retest` as "rerun all pipeline runs" — it rebuilds all images
- This means `/retest` is safe to use for rebuilding expired Konflux images without re-running passing Prow jobs
- Builds typically take 10-20 minutes; re-run this command to check availability afterward

## Error Handling

| Scenario | Action |
|----------|--------|
| PR not found | Show error with PR number |
| No .tekton/ directory | `ERROR: No .tekton/ directory found. This repo may not use Konflux.` |
| No pull-request configs found | `ERROR: No pull-request Tekton pipeline configs found in .tekton/.` |
| quay.io API unreachable | `ERROR: Unable to reach quay.io registry API.` |
| Image expired | Report as NOT FOUND, show `--` in Expires Date column, offer to retrigger via `/retest` if PR is still open |
| PR is closed/merged | Do not offer retrigger; note that images can only be rebuilt on open PRs |

## Important Notes

- PR images have an expiration date set by quay.io. Query the tag API to get the exact expiry date per image.
- For images not found (not triggered or already expired/garbage-collected), show `--` in the Expires Date column.
- Expired images can be rebuilt by commenting `/retest` on the PR (only works if PR is still open)
- Image URL patterns **vary between components** — always read from the Tekton config
- Not all pipelines trigger on every PR — each has a CEL expression filtering on changed file paths

## Requirements

- `gh` CLI authenticated with access to the target repository
- `curl` available
