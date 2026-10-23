#!/usr/bin/env bash
#
# capi-sync-provider.sh — sync CAPI provider API types into pkg/capi/<provider>/
#
# The synced module ends up with no dependency on sigs.k8s.io/cluster-api: the
# CAPI declarations its types need are extracted into pkg/capi/<provider>/capicore/
# from the CAPI version this provider resolves to. See the "CAPI Provider Types"
# section of DEVELOPMENT.md for why that matters.
#
# Which API versions a provider contributes is declared in
# hack/capi-vendor/<provider>/vendor_imports.go and read from there, so adding
# or removing one does not require touching this script.
#
# To update a CAPI provider to a new upstream version:
#   1. Edit hack/capi-vendor/<provider>/go.mod to the new version
#   2. Add or remove API versions in hack/capi-vendor/<provider>/vendor_imports.go
#   3. Run: make capi-sync-force
#   4. Verify CRDs regenerated correctly: make generate
#   5. Re-vendor: make deps
#   6. Run tests: make test
#
# To re-sync all providers (e.g. after modifying the copy tool):
#   make capi-sync-force
#
set -euo pipefail

command -v jq >/dev/null 2>&1 || { echo "ERROR: jq is required but not found in PATH" >&2; exit 1; }

PROVIDER="${1:?usage: capi-sync-provider.sh <provider>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOD_DIR="$ROOT/hack/capi-vendor/$PROVIDER"
DST_DIR="$ROOT/pkg/capi/$PROVIDER"
COPY_TOOL="$ROOT/hack/copy-capi-types/main.go"
BOILERPLATE="$ROOT/hack/boilerplate.go.txt"
CONTROLLER_GEN="${CONTROLLER_GEN:-controller-gen}"

# capi-workspace-run.sh runs the command from a temporary directory, so a
# relative path containing a slash would no longer resolve. Bare names are
# left alone; they are found via PATH, which is unaffected by the directory.
if [[ "$CONTROLLER_GEN" == */* && "$CONTROLLER_GEN" != /* ]]; then
    CONTROLLER_GEN="$(cd "$(dirname "$CONTROLLER_GEN")" && pwd)/$(basename "$CONTROLLER_GEN")"
fi

run_go() {
    # The copier only uses the standard library. Run outside module mode so
    # syncing also works before vendoring reflects new local replacements.
    GO111MODULE=off GOWORK=off GOFLAGS='' go "$@"
}

# Download a module into the cache and print its directory.
mod_cache_dir() {
    local module="$1"
    local dir
    dir=$( (cd "$MOD_DIR" && GO111MODULE=on GOWORK=off GOFLAGS='' go mod download -json "$module") | jq -r '.Dir')
    if [[ -z "$dir" || "$dir" == "null" ]]; then
        echo "ERROR: failed to resolve module cache directory for $module" >&2
        exit 1
    fi
    echo "$dir"
}

copy_types() {
    local src="$1" dst="$2"
    shift 2
    rm -rf "$dst"
    run_go run "$COPY_TOOL" --src "$src" --dst "$dst" "$@"
}

# Map provider names to their upstream module paths.
upstream_module() {
    case "$1" in
        agent)    echo "github.com/openshift/cluster-api-provider-agent/api" ;;
        aws)      echo "sigs.k8s.io/cluster-api-provider-aws/v2" ;;
        azure)    echo "sigs.k8s.io/cluster-api-provider-azure" ;;
        gcp)      echo "sigs.k8s.io/cluster-api-provider-gcp" ;;
        ibmcloud) echo "sigs.k8s.io/cluster-api-provider-ibmcloud" ;;
        kubevirt) echo "sigs.k8s.io/cluster-api-provider-kubevirt" ;;
        openstack) echo "sigs.k8s.io/cluster-api-provider-openstack" ;;
        *) echo "ERROR: upstream_module: unknown provider: $1" >&2; exit 1 ;;
    esac
}

# Import prefixes the copier may keep for a given package, beyond its built-in
# allowlist. Keyed by "<provider>:<package>". Everything else is stripped.
allow_prefixes() {
    case "$1" in
        aws:api/v1beta1|aws:exp/api/v1beta1|aws:exp/api/v1beta2)
            echo "sigs.k8s.io/cluster-api-provider-aws/v2/api/" ;;
        kubevirt:api/v1alpha1)
            echo "kubevirt.io/api" ;;
        openstack:api/v1beta1)
            echo "sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/" ;;
        openstack:api/v1alpha1)
            echo "sigs.k8s.io/cluster-api-provider-openstack/api/"
            echo "sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/" ;;
    esac
}

# Non-API packages to copy as well, because the API types use them at runtime.
# Copied before the API packages that reference them.
support_packages() {
    case "$1" in
        azure)     echo "util/azure" ;;
        openstack) echo "pkg/utils/optional"; echo "pkg/utils/errors" ;;
    esac
}

# Filename globs to delete after copying.
prune_globs() {
    case "$1" in
        # ROSA/EKS/Fargate/Managed types — HyperShift doesn't use them and
        # stripping leaves broken cross-references to banned rosacontrolplane
        # imports. webhooks.go and s3bucket.go lose their callers, leaving
        # aggregateObjErrors and validateS3BucketName dead. awscluster_defaults.go
        # only wraps a skipped generated defaulter.
        aws) echo "rosa*"; echo "awsfargate*"; echo "awsmanaged*"
             echo "webhooks.go"; echo "s3bucket.go"; echo "awscluster_defaults.go" ;;
    esac
}

# The API packages to copy are read from the provider's vendor_imports.go rather
# than listed here, so adding or removing an API version is a one-line change in
# that file and cannot drift from what the module actually pulls in.
provider_packages() {
    local imports="$MOD_DIR/vendor_imports.go"
    [[ -f "$imports" ]] || { echo "ERROR: $imports not found" >&2; exit 1; }
    local pkgs
    pkgs=$(sed -n 's/^[[:space:]]*_ "\(.*\)"$/\1/p' "$imports")
    [[ -n "$pkgs" ]] || { echo "ERROR: no packages declared in $imports" >&2; exit 1; }
    echo "$pkgs"
}

MODULE_PATH=$(upstream_module "$PROVIDER")
SRC=$(mod_cache_dir "$MODULE_PATH")

# Drop everything previously copied, so that removing an API version from
# vendor_imports.go actually removes it rather than leaving a stale package
# behind. go.mod and go.sum are maintained by go mod tidy below, and .synced is
# the make stamp; neither is produced by the copy.
find "$DST_DIR" -mindepth 1 -maxdepth 1 \
    ! -name 'go.mod' ! -name 'go.sum' ! -name '.synced' -exec rm -rf {} +

while read -r sub; do
    [[ -n "$sub" ]] || continue
    copy_types "$SRC/$sub" "$DST_DIR/$sub"
done < <(support_packages "$PROVIDER")

while read -r pkg; do
    [[ -n "$pkg" ]] || continue
    sub="${pkg#"$MODULE_PATH"/}"
    if [[ "$sub" == "$pkg" ]]; then
        echo "ERROR: $pkg is not within module $MODULE_PATH" >&2
        exit 1
    fi
    allow_args=()
    while read -r prefix; do
        [[ -n "$prefix" ]] || continue
        allow_args+=(--allow "$prefix")
    done < <(allow_prefixes "$PROVIDER:$sub")
    copy_types "$SRC/$sub" "$DST_DIR/$sub" ${allow_args[@]+"${allow_args[@]}"}
done < <(provider_packages)

while read -r glob; do
    [[ -n "$glob" ]] || continue
    find "$DST_DIR" -name "$glob" -exec rm -f {} +
done < <(prune_globs "$PROVIDER")

# Remove upstream test files and conversion machinery — HyperShift uses the
# copied types for CRD generation and controller references, not for CAPI's
# conversion webhooks or upstream tests.
find "$DST_DIR" \( -name '*_test.go' -o -name '*conversion*' \) -exec rm -f {} +

# Extract the CAPI declarations these types need into the provider module and
# repoint the imports at them, so the module carries no dependency on
# sigs.k8s.io/cluster-api. Driven by the CAPI version this provider resolves
# to, which differs between providers and must stay independent of the root
# module's.
echo "Localizing CAPI types for $PROVIDER..."
CAPI_SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api")
rm -rf "$DST_DIR/capicore"
run_go run "$COPY_TOOL" --localize \
    --dir "$DST_DIR" \
    --module "$MODULE_PATH" \
    --capi-root "$CAPI_SRC"

echo "Running controller-gen for deepcopy..."

# controller-gen must resolve types across the main module and each
# pkg/capi/<provider> module, which capi-workspace-run.sh sets up.
"$ROOT/hack/capi-workspace-run.sh" \
    "$CONTROLLER_GEN" object:headerFile="$BOILERPLATE" paths="$MODULE_PATH/..."

# Drop requirements the localized types no longer need. Leaving
# sigs.k8s.io/cluster-api here would still raise the root module's floor for it
# through MVS, which is the coupling this is meant to remove.
echo "Tidying $PROVIDER module..."
(cd "$DST_DIR" && GOWORK=off GOFLAGS='' go mod tidy)

echo "Done syncing $PROVIDER"
