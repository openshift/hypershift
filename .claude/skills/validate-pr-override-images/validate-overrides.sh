#!/bin/bash
# validate-overrides.sh
# Parses a PR description for the override contract (branch: X.Y wants: PR-links),
# extracts override images from the diff, and validates each image contains the claimed PRs.
# Usage: ./validate-overrides.sh <pr-number> [repo]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: $0 <pr-number> [repo]" >&2
  exit 2
fi

PR="$1"
GH_REPO="${2:-openshift/hypershift}"

echo "=== Validating CPO override images for PR #${PR} ==="
echo ""

# Step 1: Parse PR description for the contract
echo "--- Step 1: Parsing PR description ---"
BODY=$(gh pr view "$PR" --repo "$GH_REPO" --json body -q .body | tr -d '\r')

BRANCH_LIST=""
FOUND_LINES=0
in_code_block=false

while IFS= read -r line; do
  if [[ "$line" == '```'* ]]; then
    if $in_code_block; then
      in_code_block=false
    else
      in_code_block=true
    fi
    continue
  fi
  if $in_code_block; then
    continue
  fi

  lower_line=$(echo "$line" | tr '[:upper:]' '[:lower:]')
  if [[ ! "$lower_line" == *branch:*wants:* ]]; then
    continue
  fi

  branch=$(echo "$line" | sed -n 's/^[[:space:]]*[bB][rR][aA][nN][cC][hH]:[[:space:]]*\([0-9]*\.[0-9]*\)[[:space:]]*[wW][aA][nN][tT][sS]:[[:space:]]*\(.*\)$/\1/p')
  wants=$(echo "$line" | sed -n 's/^[[:space:]]*[bB][rR][aA][nN][cC][hH]:[[:space:]]*[0-9]*\.[0-9]*[[:space:]]*[wW][aA][nN][tT][sS]:[[:space:]]*\(.*\)$/\1/p')

  if [[ -n "$branch" && -n "$wants" ]]; then
    FOUND_LINES=$((FOUND_LINES + 1))
    pr_numbers=""
    for url in $(echo "$wants" | tr ',' ' '); do
      url=$(echo "$url" | xargs)
      num=$(echo "$url" | grep -oE '[0-9]+$' || true)
      if [[ -n "$num" ]]; then
        if [[ -n "$pr_numbers" ]]; then
          pr_numbers="$pr_numbers $num"
        else
          pr_numbers="$num"
        fi
      fi
    done
    if [[ -z "$pr_numbers" ]]; then
      echo "ERROR: branch $branch has 'wants:' but no valid PR numbers could be parsed"
      exit 1
    fi
    BRANCH_LIST="${BRANCH_LIST}${branch}=${pr_numbers}
"
    echo "  branch $branch wants PRs: $pr_numbers"
  fi
done <<< "$BODY"

if [[ $FOUND_LINES -eq 0 ]]; then
  echo ""
  echo "ERROR: No 'branch: X.Y wants: <PR-links>' lines found in PR description."
  echo ""
  echo "The PR description must include lines like:"
  echo "  branch: 4.19 wants: https://github.com/openshift/hypershift/pull/1234"
  echo "  branch: 4.20 wants: https://github.com/openshift/hypershift/pull/1234, https://github.com/openshift/hypershift/pull/5678"
  exit 1
fi

echo ""

# Step 2: Extract images per branch from the diff
echo "--- Step 2: Extracting override images from diff ---"
DIFF=$(gh pr diff "$PR" --repo "$GH_REPO")

IMAGE_LIST=""
current_version=""

while IFS= read -r line; do
  version_match=$(echo "$line" | sed -n 's/^[+ ].*version:[[:space:]]*\([0-9]*\.[0-9]*\)\.[0-9]*.*/\1/p')
  if [[ -n "$version_match" ]]; then
    current_version="$version_match"
  fi

  image_match=$(echo "$line" | sed -n 's/^+.*cpoImage:[[:space:]]*\(.*\)/\1/p')
  image_match="${image_match#"${image_match%%[![:space:]]*}"}"
  image_match="${image_match%"${image_match##*[![:space:]]}"}"
  if [[ -n "$image_match" && -n "$current_version" ]]; then
    entry="${current_version}=${image_match}"
    if [[ "$IMAGE_LIST" != *"$entry"* ]]; then
      IMAGE_LIST="${IMAGE_LIST}${entry}
"
    fi
  fi
done <<< "$DIFF"

echo "$IMAGE_LIST" | while IFS= read -r entry; do
  if [[ -n "$entry" ]]; then
    branch="${entry%%=*}"
    image="${entry#*=}"
    echo "  branch $branch image: $image"
  fi
done

echo ""

# Step 3: Validate each (branch, image, PR) tuple
echo "--- Step 3: Validating images contain claimed PRs ---"
echo ""

echo "$BRANCH_LIST" | while IFS= read -r branch_entry; do
  if [[ -z "$branch_entry" ]]; then
    continue
  fi
  branch="${branch_entry%%=*}"
  prs="${branch_entry#*=}"

  branch_images=$(echo "$IMAGE_LIST" | grep "^${branch}=" | sed "s/^${branch}=//" | sort -u)

  if [[ -z "$branch_images" ]]; then
    echo "WARNING: branch $branch declared in description but no override images found in diff"
    echo "FAILURE_COUNT:1"
    continue
  fi

  echo "$branch_images" | while IFS= read -r image; do
    if [[ -z "$image" ]]; then
      continue
    fi
    echo "Image: $image (branch $branch)"
    for pr_num in $prs; do
      verify_output=$("$SCRIPT_DIR/verify-pr-in-image.sh" "$image" "$pr_num" "$REPO_ROOT" 2>&1) && verify_rc=0 || verify_rc=$?
      echo "$verify_output" | sed 's/^/    /'
      if echo "$verify_output" | tail -1 | grep -q "PASS"; then
        echo "  PR #${pr_num}: PASS"
        echo "PASS_COUNT:1"
      else
        echo "  PR #${pr_num}: FAIL"
        echo "FAILURE_COUNT:1"
      fi
    done
    echo ""
  done
done > /tmp/validate-overrides-output.$$

grep -v "COUNT:" /tmp/validate-overrides-output.$$
PASSES=$(grep -c "PASS_COUNT:" /tmp/validate-overrides-output.$$ || true)
FAILURES=$(grep -c "FAILURE_COUNT:" /tmp/validate-overrides-output.$$ || true)
rm -f /tmp/validate-overrides-output.$$

# Summary
echo "=== Summary ==="
echo "Passed: $PASSES"
echo "Failed: $FAILURES"

if [[ $FAILURES -gt 0 ]]; then
  echo ""
  echo "OVERALL: FAIL"
  exit 1
else
  echo ""
  echo "OVERALL: PASS"
fi
