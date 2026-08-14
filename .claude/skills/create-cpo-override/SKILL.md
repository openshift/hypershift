---
description: Interactively create CPO image overrides — resolves images, verifies fixes, edits overrides.yaml, and prepares a PR
---

# Create CPO Override

## Synopsis
```text
/create-cpo-override
```

## Description

Interactively creates control-plane-operator (CPO) image override entries in
`hypershift-operator/controlplaneoperator-overrides/assets/overrides.yaml`.
Automates image discovery, fix verification, YAML editing, and PR preparation
so the result is compatible with `/validate-pr-override-images`.

## Prerequisites

- `skopeo` installed
- `oc` installed (for `oc adm release info`)
- `gh` CLI authenticated
- Local git repo has relevant release branches fetched (`git fetch --all`)
- Internet access to quay.io and the Cincinnati API

## Implementation

Follow the steps below **in order**. Each step builds on the previous one.
Use `AskUserQuestion` for multi-choice prompts where indicated. For free-form
inputs, ask the user directly in conversation.

---

### Step 0 — Detect pull secret

Many OCP payload images require authentication. Before any image operations,
probe for a working pull secret:

1. Try a lightweight `skopeo inspect` against a known release payload image
   (e.g. `quay.io/openshift-release-dev/ocp-release:4.18.0-multi`). If it
   succeeds, the container runtime's default auth is sufficient — no extra
   flags needed.
2. If that fails, check if `$PULL_SECRET` is set and points to an existing
   file. If so, retry with `--authfile "$PULL_SECRET"` (and use
   `-a "$PULL_SECRET"` for `oc adm release info`). Use this authfile for all
   subsequent image operations.
3. If neither works, ask the user to provide a path to a pull secret file.
   If they don't have one, warn that payload-sourced images (Sources 1 & 2)
   will be unavailable and only Konflux images (Source 3, public on
   `quay.io/redhat-user-workloads/`) can be resolved automatically.

---

### Step 1 — Gather inputs

Collect the following from the user. Ask all questions up front (or in logical
groups) rather than one at a time.

#### 1a. Platform(s)

Ask the user which platform(s) to override.

Options: `aws`, `azure`, or `both`.

#### 1b. Jira ticket

Ask for the parent Jira ticket that motivates the override (e.g. `OCPBUGS-86238`).
This is used in comment markers and the commit message.

#### 1c. Branches

Ask which OCP minor branches need overrides (e.g. `4.20, 4.21`).

#### 1d. Version range per branch

For each branch, ask the user which z-stream versions to override. Accepted
formats:

| Input | Meaning |
|-------|---------|
| `4.20.0-4.20.24` | Explicit range |
| `all` | Every z-stream from `X.Y.0` through the highest z-stream in the Cincinnati `fast-X.Y` channel |
| `4.20.15` | Single version |

When the user says **`all`**, resolve the range as follows:

```bash
curl -sH 'Accept: application/json' \
  "https://api.openshift.com/api/upgrades_info/v1/graph?channel=fast-${BRANCH}&arch=multi" \
  | python3 -c "
import json, sys
data = json.load(sys.stdin)
versions = [n['version'] for n in data.get('nodes', [])
            if n['version'].startswith('${BRANCH}.')]
zs = sorted(int(v.split('.')[2]) for v in versions)
print(max(zs))
"
```

Then generate every z-stream from `X.Y.0` through `X.Y.<max_z>` **inclusive**
(Cincinnati skips some z-streams like 4.20.7, 4.20.9 — clusters can still run
them, so include them all).

#### 1d-ii. Verify the next z-stream via development cutoff dates

**This is critical to avoid upgrade regressions.** If a customer is on an
overridden version (e.g. 4.22.3) and upgrades to the next z-stream (e.g.
4.22.4), the override no longer applies. If that next z-stream does **not**
contain the fix in its payload, the customer regresses.

To determine whether the next z-stream after the override range needs to be
included, check the **development cutoff date** for that version against the
PR merge dates.

**If the `productpages` MCP server is available**, use it:

1. Search for the z-stream release entity:
   ```
   search_entities(q="OpenShift X.Y.z", kind="release")
   ```
2. Browse the schedule for the next z-stream's development cutoff:
   ```
   browse_schedule(entity_id=<id>, q="X.Y.<max_z+1>")
   ```
3. Find the task named `X.Y.<max_z+1> Development Cut Off` and note its
   `date_finish`.
4. Compare with each required PR's merge date (from `gh pr view --json mergedAt`).

If **all** PRs merged **before** the cutoff date, the next z-stream will
include the fixes — no override needed for it. Add a comment in
`overrides.yaml` noting this (e.g. "4.22.4 does not need an override: both
PRs merged before the development cutoff").

If **any** PR merged **after** the cutoff date, extend the override range to
include that z-stream and repeat the check for the one after it.

**If the `productpages` MCP server is NOT available**, warn the user and ask
for the cutoff date:

> ⚠️ **Cannot verify development cutoff dates** — the Product Pages MCP server
> is not connected. Please check the development cutoff date for
> `X.Y.<max_z+1>` at https://pp.engineering.redhat.com and provide it here so
> I can verify the override range is complete.

When the user provides the cutoff date, compare it against the PR merge dates
(same logic as above). If any PR merged after the cutoff, extend the override
range and ask for the next z-stream's cutoff date. Repeat until the range is
safe.

#### 1e. Required PRs

Ask which GitHub PRs must be present in the override image. Accept PR URLs
(e.g. `https://github.com/openshift/hypershift/pull/8593`) or plain numbers
(e.g. `8593`). Multiple PRs can be comma-separated.

PRs can be specified per-branch or globally (applied to all branches). If a PR
is a cherry-pick, the user should provide the **branch-specific** PR number.

#### 1f. Image per branch (optional)

Ask whether the user wants to provide specific images or have them
auto-resolved. If auto-resolved, proceed to Step 2.

---

### Step 2 — Auto-resolve images

For each branch that needs an image, try sources **in this order**. Stop at
the first source whose image contains **all** required PRs for that branch.

#### Source 1: Latest stable payload

```bash
# Get the latest version in stable-X.Y
LATEST=$(curl -sH 'Accept: application/json' \
  "https://api.openshift.com/api/upgrades_info/v1/graph?channel=stable-${BRANCH}&arch=multi" \
  | python3 -c "
import json, sys
data = json.load(sys.stdin)
versions = [n['version'] for n in data.get('nodes', [])
            if n['version'].startswith('${BRANCH}.')]
versions.sort(key=lambda v: [int(x) for x in v.split('.')])
print(versions[-1])
")

# Get the hypershift component's source commit from the payload using JSON output.
# This avoids needing skopeo access to the internal ocp-v4.0-art-dev repo.
COMMIT=$(oc adm release info \
  ${AUTHFILE:+-a "$AUTHFILE"} \
  "quay.io/openshift-release-dev/ocp-release:${LATEST}-multi" \
  -o json 2>/dev/null \
  | jq -r '.references.spec.tags[] | select(.name == "hypershift") | .annotations["io.openshift.build.commit.id"]')
```

Verify all required PRs are ancestors of `$COMMIT` using
`git merge-base --is-ancestor`. If they are, the fix is already in the latest
stable payload — **no override is needed for this branch**. Report this to
the user and skip the branch.

Note: do **not** use the payload's internal image pullspec (`ocp-v4.0-art-dev`)
as the override image itself — that repo requires special registry access that
clusters may not have. If an override is still needed for older z-streams,
proceed to Source 3 (Konflux).

#### Source 2: Latest fast payload

Same as Source 1 but query `fast-${BRANCH}` instead of `stable-${BRANCH}`.
If the fix is already in fast, report it and skip the branch.

#### Source 3: Konflux push build

```bash
REPO="quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-${BRANCH_HYPHEN}"
# BRANCH_HYPHEN is e.g. "4-21" (dots replaced with hyphens)

# List tags, filter for 40-char hex commit SHA tags (push builds)
TAGS=$(skopeo list-tags "docker://${REPO}" 2>/dev/null \
  | python3 -c "
import json, sys, re
tags = json.load(sys.stdin).get('Tags', [])
# Push build tags are 40-char hex, no suffix
commits = [t for t in tags if re.fullmatch(r'[0-9a-f]{40}', t)]
for c in commits:
    print(c)
")
```

For each candidate commit tag (check newest first — use `git log` to order
them), verify the required PRs are ancestors:

```bash
for PR_NUM in $REQUIRED_PRS; do
  PR_MERGE=$(gh pr view "$PR_NUM" --repo openshift/hypershift \
    --json mergeCommit --jq '.mergeCommit.oid // empty')
  git merge-base --is-ancestor "$PR_MERGE" "$COMMIT_TAG"
done
```

If all PRs pass, get the digest-pinned reference:

```bash
DIGEST=$(skopeo inspect --override-os linux --override-arch amd64 \
  "docker://${REPO}:${COMMIT_TAG}" 2>/dev/null \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['Digest'])")
IMAGE="${REPO}@${DIGEST}"
```

Source label: `Konflux push build (commit ${COMMIT_TAG:0:12})`.

#### Source 4: No image found

If none of the above sources have an image containing all required PRs:

1. Report which sources were tried and why they failed
2. Point the user to:
   - The `build-cpo-image` dev skill for building a custom image locally
   - The Konflux hotfix process in `contrib/konflux/README.md`
3. Ask the user to provide an image reference manually
4. When they do, verify it (Step 3) and continue

---

### Step 3 — Verify PRs are present in images

For each `(branch, image, PR)` tuple, run the existing verification script:

```bash
.claude/skills/validate-pr-override-images/verify-pr-in-image.sh \
  "$IMAGE" "$PR_NUM" "$(git rev-parse --show-toplevel)"
```

This script:
1. Uses `skopeo inspect` to get the image's `vcs-ref` label (git commit)
2. Uses `gh pr view` to get the PR's merge commit
3. Checks `git merge-base --is-ancestor <pr-merge-commit> <image-commit>`

If any verification fails, report the failure and ask the user how to proceed
(provide a different image, skip that branch, or abort).

---

### Step 4 — Test image pullability

For each resolved image, confirm it is pullable (use the authfile from Step 0
if one was detected):

```bash
skopeo inspect --override-os linux --override-arch amd64 \
  ${AUTHFILE:+--authfile "$AUTHFILE"} \
  "docker://${IMAGE}" > /dev/null 2>&1
```

Report any failures.

---

### Step 5 — Present summary for confirmation

Display a summary table and ask the user to confirm before making changes:

```
=== CPO Override Summary ===

Ticket: OCPBUGS-XXXXX
Platform(s): azure

Branch 4.20:
  Versions: 4.20.0 - 4.20.27 (28 entries)
  Image: quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-4-20@sha256:155e4e...
  Source: Konflux push build (commit 46f3b353fd5c)
  PRs verified: #8593 PASS, #8565 PASS

Branch 4.21:
  Versions: 4.21.0 - 4.21.18 (19 entries)
  Image: quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-4-21@sha256:1b3f1b...
  Source: Konflux push build (commit c84f8073)
  PRs verified: #8565 PASS

Branch 4.22:
  No override needed — fix landed before GA (PR #8564, commit d6c72d15)

Proceed with editing overrides.yaml? [Yes / No]
```

---

### Step 6 — Edit `overrides.yaml`

Modify `hypershift-operator/controlplaneoperator-overrides/assets/overrides.yaml`.

**Placement rules:**
- Add entries under the correct platform key (`aws` or `azure`)
- Append new override entries **before** the `testing:` section
- If entries for the same ticket+branch already exist, replace them

**Format:** Read the existing `overrides.yaml` and replicate its conventions
exactly — comment markers, indentation, and entry structure. Do **not**
hard-code a format here; the file itself is the source of truth.

---

### Step 7 — Run unit tests

```bash
go test ./hypershift-operator/controlplaneoperator-overrides/...
```

If tests fail, fix the YAML and re-run. Common issues:
- YAML parse errors (bad indentation)
- Duplicate version entries across different ticket sections

---

### Step 8 — Prepare commit and PR description

#### Commit

Stage only the override file (and any new Konflux PDS files if needed):

```bash
git add hypershift-operator/controlplaneoperator-overrides/assets/overrides.yaml
# If Konflux PDS files were created:
# git add contrib/konflux/cpo_X_Y_stream.yaml
```

Commit message format:

```
<TICKET>: add CPO overrides for <short description>

Add <platform> CPO image overrides for <branches> to fix <description>.

<Branch that doesn't need override> does not need an override: the fix
(PR #NNNN, commit <short-sha>) landed before <version> and will be
included in the GA release.

- X.Y.0-X.Y.Z: <TICKET-per-branch> (PR #NNNN cherry-pick to release-X.Y)
- ...

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
```

#### PR description

The PR description **must** include validation contract lines that
`/validate-pr-override-images` parses. The contract format is documented in
[docs/content/contribute/cpo-overrides.md](../../../../docs/content/contribute/cpo-overrides.md)
under "Validating Override Images Contain Claimed PRs". Generate those lines
from the branch-to-PRs mapping collected in Step 1.

Include a Summary section, the contract lines (outside code blocks so the
validator can parse them), and a Test plan section.

#### Post-PR validation

After creating the PR, run `/validate-pr-override-images <pr-number>` to
confirm the PR passes validation end-to-end. If it fails, fix the PR
description or image references and re-run until it passes.
