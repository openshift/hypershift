#!/usr/bin/env bash
# gha-rehearse.sh — rehearse new GitHub Actions workflows from a PR branch.
#
# GitHub Actions workflow_dispatch lets you pick a branch to run from, but
# the workflow file must already exist on the default branch for it to appear
# in the UI. This script pushes minimal stubs to the default branch so that
# a new or renamed workflow from a PR can be rehearsed before merge.
#
# Usage:
#   contrib/ci/gha-rehearse.sh setup <PR-number>   Push stubs to default branch
#   contrib/ci/gha-rehearse.sh cleanup             Revert the stub commit
#
# After "setup", trigger the workflow from the Actions UI (or gh workflow run)
# selecting the PR's head branch.
#
# Requirements: gh CLI, git

set -euo pipefail

readonly REPO="${GHA_REHEARSE_REPO:-$(gh repo view --json nameWithOwner -q .nameWithOwner)}"
readonly DEFAULT_BRANCH="${GHA_REHEARSE_DEFAULT_BRANCH:-$(gh repo view --json defaultBranchRef -q .defaultBranchRef.name)}"
readonly MARKER_FILE=".github/.gha-rehearse-marker"

die() { echo "error: $*" >&2; exit 1; }

generate_stub() {
    local -r name="$1"
    cat <<EOF
name: ${name}
on:
  workflow_dispatch:
jobs:
  stub:
    runs-on: ubuntu-latest
    steps:
      - run: echo "This is a rehearsal stub. Select the PR branch to run the real workflow."
EOF
}

cmd_setup() {
    local -r pr_number="${1:?usage: gha-rehearse.sh setup <PR-number>}"

    local -r pr_json="$(gh pr view "$pr_number" --repo "$REPO" --json headRefName,files)"
    local -r head_branch="$(echo "$pr_json" | jq -r '.headRefName')"

    local -a workflow_files=()
    while IFS= read -r path; do
        workflow_files+=("$path")
    done < <(echo "$pr_json" | jq -r '.files[].path' | grep '^\.github/workflows/.*\.ya\?ml$')

    if [[ ${#workflow_files[@]} -eq 0 ]]; then
        die "PR #${pr_number} has no workflow file changes"
    fi

    local -a new_workflows=()
    for wf in "${workflow_files[@]}"; do
        if ! git cat-file -e "origin/${DEFAULT_BRANCH}:${wf}" 2>/dev/null; then
            new_workflows+=("$wf")
        fi
    done

    if [[ ${#new_workflows[@]} -eq 0 ]]; then
        echo "All changed workflow files already exist on ${DEFAULT_BRANCH}."
        echo "You can rehearse directly: gh workflow run <name> --ref ${head_branch}"
        exit 0
    fi

    echo "New workflow files to stub on ${DEFAULT_BRANCH}:"
    printf "  %s\n" "${new_workflows[@]}"
    echo

    local -r tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT

    git fetch origin "pull/${pr_number}/head" --quiet 2>/dev/null || true

    for wf in "${new_workflows[@]}"; do
        local name
        name="$(git show "FETCH_HEAD:${wf}" 2>/dev/null \
            | grep -m1 '^name:' | sed 's/^name:\s*//' || echo "${wf##*/}")"
        mkdir -p "${tmpdir}/$(dirname "$wf")"
        generate_stub "$name" > "${tmpdir}/${wf}"
    done

    mkdir -p "${tmpdir}/.github"
    printf '%s\n' "${new_workflows[@]}" > "${tmpdir}/${MARKER_FILE}"

    echo "Pushing stubs to ${DEFAULT_BRANCH}..."
    git fetch origin "${DEFAULT_BRANCH}" --quiet

    local -r index_file="${tmpdir}/index"
    GIT_INDEX_FILE="$index_file" git read-tree "origin/${DEFAULT_BRANCH}"

    for wf in "${new_workflows[@]}"; do
        local blob
        blob="$(git hash-object -w "${tmpdir}/${wf}")"
        GIT_INDEX_FILE="$index_file" git update-index --add --cacheinfo "100644,${blob},${wf}"
    done

    local -r marker_blob="$(git hash-object -w "${tmpdir}/${MARKER_FILE}")"
    GIT_INDEX_FILE="$index_file" git update-index --add --cacheinfo "100644,${marker_blob},${MARKER_FILE}"

    local -r new_tree="$(GIT_INDEX_FILE="$index_file" git write-tree)"
    local -r stub_commit="$(git commit-tree "$new_tree" -p "origin/${DEFAULT_BRANCH}" \
        -m "gha-rehearse: add workflow stubs for PR #${pr_number}

Temporary stubs to enable workflow_dispatch rehearsal.
Run 'contrib/ci/gha-rehearse.sh cleanup' to revert.")"

    git push origin "${stub_commit}:refs/heads/${DEFAULT_BRANCH}"

    echo
    echo "Done. Stubs pushed to ${DEFAULT_BRANCH}."
    echo
    echo "To rehearse, run:"
    for wf in "${new_workflows[@]}"; do
        local stub_name
        stub_name="$(grep -m1 '^name:' "${tmpdir}/${wf}" | sed 's/^name:\s*//')"
        echo "  gh workflow run '${stub_name}' --repo ${REPO} --ref ${head_branch}"
    done
    echo
    echo "When finished, run: contrib/ci/gha-rehearse.sh cleanup"
}

cmd_cleanup() {
    git fetch origin "${DEFAULT_BRANCH}" --quiet

    if ! git cat-file -e "origin/${DEFAULT_BRANCH}:${MARKER_FILE}" 2>/dev/null; then
        die "no rehearsal stubs found on ${DEFAULT_BRANCH} (missing ${MARKER_FILE})"
    fi

    local -r head_msg="$(git log -1 --format=%s "origin/${DEFAULT_BRANCH}")"
    if [[ "$head_msg" != gha-rehearse:* ]]; then
        die "HEAD of ${DEFAULT_BRANCH} is not a rehearsal stub commit: ${head_msg}"
    fi

    echo "Reverting stub commit on ${DEFAULT_BRANCH}..."
    local -r parent="$(git rev-parse "origin/${DEFAULT_BRANCH}~1")"
    git push origin "${parent}:refs/heads/${DEFAULT_BRANCH}"

    echo "Done. Stubs removed from ${DEFAULT_BRANCH}."
}

case "${1:-}" in
    setup)   shift; cmd_setup "$@" ;;
    cleanup) shift; cmd_cleanup "$@" ;;
    *)       echo "usage: $0 {setup <PR-number> | cleanup}" >&2; exit 1 ;;
esac
