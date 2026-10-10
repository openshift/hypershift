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

# Some override images reference a private mirror of the OCP release repos
# (e.g. the ARO ACR mirror) that is not publicly pullable. The identical
# content (including the commit labels) is available on quay.io, so inspect
# the public copy while still reporting the real override image ($IMAGE).
INSPECT_IMAGE="$IMAGE"
case "$INSPECT_IMAGE" in
  arohcpocpprod.azurecr.io/*) INSPECT_IMAGE="quay.io/${INSPECT_IMAGE#*/}" ;;
esac
if [[ "$INSPECT_IMAGE" != "$IMAGE" ]]; then
  echo "Note: inspecting public mirror copy $INSPECT_IMAGE"
fi

echo "Inspecting image..."
INSPECT=$(skopeo inspect --no-tags --override-os linux --override-arch amd64 "${AUTHFILE_ARGS[@]}" "docker://$INSPECT_IMAGE") || {
  echo "ERROR: Could not inspect image $INSPECT_IMAGE"
  exit 1
}

# Determine the source commit the image was built from. Konflux images record
# the public openshift/hypershift commit in "vcs-ref". Release-payload images
# (ocp-v4.0-art-dev) instead put the internal ART build commit in "vcs-ref"
# (which is not a public commit) and record the real source commit in
# "io.openshift.build.commit.id". Try vcs-ref first (preserves existing
# behavior) and fall back to the build commit id, using whichever resolves to
# a commit present in the repo.
VCS_REF=$(echo "$INSPECT" | grep -o '"vcs-ref"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*:[[:space:]]*"\([^"]*\)".*/\1/')
BUILD_COMMIT=$(echo "$INSPECT" | grep -o '"io.openshift.build.commit.id"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*:[[:space:]]*"\([^"]*\)".*/\1/')

if [[ -z "$VCS_REF" && -z "$BUILD_COMMIT" ]]; then
  echo "ERROR: Could not find vcs-ref or io.openshift.build.commit.id label in image $IMAGE"
  exit 1
fi

resolve_commit() {
  local c
  for c in "$VCS_REF" "$BUILD_COMMIT"; do
    if [[ -n "$c" ]] && git -C "$REPO" cat-file -e "$c" 2>/dev/null; then
      echo "$c"
      return 0
    fi
  done
  return 1
}

COMMIT=$(resolve_commit || true)
if [[ -z "$COMMIT" ]]; then
  echo "Commit not found locally, fetching..."
  git -C "$REPO" fetch --all --quiet
  COMMIT=$(resolve_commit || true)
fi

if [[ -z "$COMMIT" ]]; then
  echo "ERROR: image source commit not found in any remote (vcs-ref=${VCS_REF:-none} io.openshift.build.commit.id=${BUILD_COMMIT:-none})"
  exit 1
fi

echo "Image source commit: $COMMIT"

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
