#!/usr/bin/env bash
#
# verify-capi-decoupling.sh — guard the invariants created by decoupling the
# CAPI provider Go modules (see "CAPI Provider Types" in DEVELOPMENT.md).
#
# Each pkg/capi/<provider> module holds a copy of an upstream provider's API
# types, with the CAPI declarations those types need extracted into a local
# `capicore/` package. Two consequences are not enforced by the ordinary build:
#
#   1. A provider module must never import upstream `sigs.k8s.io/cluster-api`.
#      Doing so silently re-couples the provider to the root module's CAPI
#      version, which is the coupling the sync exists to remove. Note that
#      `go build ./...` in the root module only compiles the subpackages of a
#      replaced module that something actually imports, so large parts of a
#      provider module can be broken and the root build stays green.
#   2. The localized `capicore` types and the upstream CAPI types they were
#      copied from are *different Go types*. Code merged from main that names
#      the upstream types fails to compile with `undefined:` or
#      `cannot use ... as ...`, and the message hints at nothing useful.
#
# This script checks both, in seconds, instead of leaving it to a 25-minute
# unit or staticcheck job.
#
# Usage: hack/verify-capi-decoupling.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

GO="${GO:-go}"
CAPI_PROVIDERS="${CAPI_PROVIDERS:-agent aws azure gcp ibmcloud kubevirt openstack}"

# Upstream CAPI API import paths, e.g. sigs.k8s.io/cluster-api/api/core/v1beta1.
# Localized capicore paths contain 'capicore' and never match this.
UPSTREAM_CAPI_RE='"sigs\.k8s\.io/cluster-api/api/[a-z0-9/]+"'

failures=0
fail() {
  failures=$((failures + 1))
  echo "FAIL: $*" >&2
}

# Directories in the main module that consume the localized provider types. The
# check below is derived from them, so it follows the migration instead of
# relying on a package list that rots.
consumer_files() {
  grep -rl 'capicore' --include='*.go' . 2>/dev/null \
    | grep -v '^\./vendor/' \
    | grep -v '^\./pkg/capi/' \
    | grep -v '^\./hack/capi-vendor/'
}

echo "==> Provider modules must not depend on upstream CAPI"
for provider in $CAPI_PROVIDERS; do
  mod="pkg/capi/$provider"
  [[ -f "$mod/go.mod" ]] || { fail "$mod/go.mod is missing"; continue; }

  # A stray require re-couples the provider even when no source imports CAPI
  # yet: MVS still raises a floor on the root module's CAPI version.
  if grep -Eq '^[[:space:]]*sigs\.k8s\.io/cluster-api([[:space:]]|/|$)' "$mod/go.mod"; then
    fail "$mod/go.mod requires sigs.k8s.io/cluster-api. Run 'make capi-sync-$provider'; if the sync could not localize a new reference, teach hack/copy-capi-types/main.go to extract it."
  fi

  if matches=$(grep -rEn "$UPSTREAM_CAPI_RE" --include='*.go' "$mod" 2>/dev/null); then
    fail "$mod imports upstream CAPI; its types must resolve to $mod/capicore"
    echo "$matches" >&2
  fi

  # Build every package in the module, not just the ones the root imports.
  # Provider modules are not vendored, so override the -mod=vendor that CI
  # sets in GOFLAGS; it would otherwise fail with "inconsistent vendoring".
  if ! (cd "$mod" && GOWORK=off GOFLAGS=-mod=readonly "$GO" build ./... 2>&1 | sed "s|^|  $provider: |" >&2); then
    fail "$provider module does not build"
  fi
done

echo
echo "==> Packages built on localized provider types must compile"
# The failure this exists for: a merge with main brings in code naming upstream
# CAPI types, git resolves it without conflict because the import block changed
# on the other side of the merge, and CI reports `undefined: clusterv1beta1`
# half an hour later. Vet the consumers of capicore here, where the answer
# arrives quickly enough to act on.
consumers=()
while IFS= read -r dir; do
  consumers+=("./$dir/...")
done < <(consumer_files | sed 's|^\./||; s|/[^/]*$||' | sort -u)

if [[ ${#consumers[@]} -gt 0 ]]; then
  if ! "$GO" vet "${consumers[@]}" 2>&1 | sed 's|^|  |' >&2; then
    fail "a package built on localized CAPI types does not compile"
    cat >&2 <<'EOF'
  If this appeared after merging or rebasing onto main, the new code most likely
  names upstream CAPI types (clusterv1beta1, capiv1, ...) whose import this
  branch removed. Point those references at the localized capicore package the
  surrounding files already use; the two declare distinct Go types and are not
  interchangeable.
EOF
  fi
fi

# Informational, not a failure: a file seeing both copies of CAPI is legitimate
# when it only handles the upstream Cluster types and the provider types
# separately, but it is where a wrong alias is most likely to be introduced.
mixed=$(while IFS= read -r file; do
  grep -Eq "$UPSTREAM_CAPI_RE" "$file" && echo "  $file"
done < <(consumer_files) || true)
if [[ -n "$mixed" ]]; then
  echo
  echo "NOTE: files using both capicore and upstream CAPI types - keep values of"
  echo "      the two type sets from crossing, or a build will fail far from here:"
  echo "$mixed"
fi

echo
if ((failures > 0)); then
  echo "$failures check(s) failed. See 'CAPI Provider Types' in DEVELOPMENT.md." >&2
  exit 1
fi
echo "CAPI decoupling invariants hold."
