#!/usr/bin/env bash
# Verifies that the tracked top-level directory list stays in sync with
# the actual git-tracked directories. This prevents the CI skip pattern
# (pipeline_skip_if_only_changed) from silently going stale when new
# top-level directories are added to the repo.
# Directories containing E2E tests directly or indirectly (e.g. test/)
# must be excluded from the regex.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TRACKED="${SCRIPT_DIR}/toplevel-dirs.txt"

# Collect top-level directories from both committed (HEAD) and staged
# (index) state, excluding hidden dirs. In CI the PR is already merged
# into HEAD; the ls-files fallback catches locally staged-but-uncommitted
# directories during local `make verify`.
ACTUAL=$( (git ls-tree -d --name-only HEAD 2>/dev/null; \
           git ls-files --cached | grep '/' | cut -d/ -f1) \
    | grep -v '^\.' | sort -u)
EXPECTED=$(sort "${TRACKED}")

DIFF=$(diff <(echo "${EXPECTED}") <(echo "${ACTUAL}") || true)

if [[ -n "${DIFF}" ]]; then
    echo "ERROR: Top-level directory list is out of sync."
    echo ""
    echo "Diff (expected vs actual):"
    echo "${DIFF}"
    echo ""
    echo "If you added or removed a top-level directory, update hack/ci/toplevel-dirs.txt."
    echo "Also update the pipeline_skip_if_only_changed regex in the ci-operator config."
    echo "Top-level directories containing E2E tests directly or indirectly (e.g. test/) must be excluded from the regex."
    exit 1
fi
