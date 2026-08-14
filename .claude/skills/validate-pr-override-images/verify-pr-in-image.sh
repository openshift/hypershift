#!/bin/bash
# verify-pr-in-image.sh
# Verifies that a container image contains a specific PR in its git history.
# Usage: ./verify-pr-in-image.sh <image> <pr-number> [repo-path]

set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "Usage: $0 <image> <pr-number> [repo-path]" >&2
  exit 2
fi

IMAGE="$1"
PR="$2"
REPO="${3:-.}"

if ! command -v skopeo &>/dev/null; then
  echo "ERROR: skopeo is not installed. Install it with: brew install skopeo (macOS) or dnf install skopeo (RHEL/Fedora)"
  exit 1
fi

AUTHFILE_PATH="${AUTHFILE:-${PULL_SECRET:-}}"
AUTHFILE_ARGS=()
if [[ -n "$AUTHFILE_PATH" && -f "$AUTHFILE_PATH" ]]; then
  AUTHFILE_ARGS=(--authfile "$AUTHFILE_PATH")
fi

echo "Inspecting image..."
INSPECT=$(skopeo inspect --no-tags --override-os linux --override-arch amd64 "${AUTHFILE_ARGS[@]}" "docker://$IMAGE") || {
  echo "ERROR: Could not inspect image $IMAGE"
  exit 1
}

COMMIT=$(echo "$INSPECT" | grep -o '"vcs-ref"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"vcs-ref"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')

if [[ -z "$COMMIT" ]]; then
  echo "ERROR: Could not find vcs-ref label in image $IMAGE"
  exit 1
fi

echo "Image commit: $COMMIT"

if ! git -C "$REPO" cat-file -e "$COMMIT" 2>/dev/null; then
  echo "Commit not found locally, fetching..."
  git -C "$REPO" fetch --all --quiet
  if ! git -C "$REPO" cat-file -e "$COMMIT" 2>/dev/null; then
    echo "ERROR: Commit $COMMIT not found in any remote"
    exit 1
  fi
fi

PR_MERGE_COMMIT=$(gh pr view "$PR" --repo openshift/hypershift --json mergeCommit --jq '.mergeCommit.oid // empty')

if [[ -z "$PR_MERGE_COMMIT" ]]; then
  echo "FAIL: PR #${PR} has no merge commit (not merged yet?)"
  exit 1
fi

echo "PR #${PR} merge commit: $PR_MERGE_COMMIT"

if ! git -C "$REPO" cat-file -e "$PR_MERGE_COMMIT" 2>/dev/null; then
  echo "Merge commit not found locally, fetching..."
  git -C "$REPO" fetch --all --quiet
  if ! git -C "$REPO" cat-file -e "$PR_MERGE_COMMIT" 2>/dev/null; then
    echo "ERROR: PR #${PR} merge commit $PR_MERGE_COMMIT not found in any remote"
    exit 1
  fi
fi

if git -C "$REPO" merge-base --is-ancestor "$PR_MERGE_COMMIT" "$COMMIT" 2>/dev/null; then
  echo "PASS: PR #${PR} is included in image $IMAGE"
else
  echo "FAIL: PR #${PR} is NOT included in image $IMAGE"
  exit 1
fi
