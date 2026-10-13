#!/usr/bin/env bash
#
# capi-sync-provider.sh — sync CAPI provider API types into pkg/capi/<provider>/
#
# The synced module ends up with no dependency on sigs.k8s.io/cluster-api: the
# CAPI declarations its types need are extracted into pkg/capi/<provider>/capicore/
# from the CAPI version this provider resolves to. See the "CAPI Provider Types"
# section of DEVELOPMENT.md for why that matters.
#
# To update a CAPI provider to a new upstream version:
#   1. Edit hack/capi-vendor/<provider>/go.mod to the new version
#   2. Run: make capi-sync-force
#   3. Verify CRDs regenerated correctly: make generate
#   4. Re-vendor: make deps
#   5. Run tests: make test
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

case "$PROVIDER" in
    agent)
        SRC=$(mod_cache_dir "github.com/openshift/cluster-api-provider-agent/api")
        copy_types "$SRC/v1alpha1" "$DST_DIR/v1alpha1"
        copy_types "$SRC/v1beta1"  "$DST_DIR/v1beta1"
        ;;
    aws)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-aws/v2")
        copy_types "$SRC/api/v1beta1" "$DST_DIR/api/v1beta1" \
            --allow sigs.k8s.io/cluster-api-provider-aws/v2/api/
        copy_types "$SRC/api/v1beta2" "$DST_DIR/api/v1beta2"
        copy_types "$SRC/exp/api/v1beta1" "$DST_DIR/exp/api/v1beta1" \
            --allow sigs.k8s.io/cluster-api-provider-aws/v2/api/
        copy_types "$SRC/exp/api/v1beta2" "$DST_DIR/exp/api/v1beta2" \
            --allow sigs.k8s.io/cluster-api-provider-aws/v2/api/
        # Remove ROSA/EKS/Fargate/Managed types — HyperShift doesn't use them and
        # stripping leaves broken cross-references to banned rosacontrolplane imports.
        # Also remove webhooks.go and s3bucket.go — their callers were stripped so
        # the remaining functions (aggregateObjErrors, validateS3BucketName) are dead code.
        # awscluster_defaults.go only wraps a skipped generated defaulter.
        find "$DST_DIR" \( -name 'rosa*' -o -name 'awsfargate*' -o -name 'awsmanaged*' \
            -o -name 'webhooks.go' -o -name 's3bucket.go' -o -name 'awscluster_defaults.go' \) -exec rm -f {} +
        ;;
    azure)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-azure")
        copy_types "$SRC/api/v1beta1" "$DST_DIR/api/v1beta1"
        ;;
    gcp)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-gcp")
        copy_types "$SRC/api/v1beta1" "$DST_DIR/api/v1beta1"
        ;;
    ibmcloud)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-ibmcloud")
        copy_types "$SRC/api/v1beta1" "$DST_DIR/api/v1beta1"
        copy_types "$SRC/api/v1beta2" "$DST_DIR/api/v1beta2"
        ;;
    kubevirt)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-kubevirt")
        copy_types "$SRC/api/v1alpha1" "$DST_DIR/api/v1alpha1" \
            --allow "kubevirt.io/api"
        ;;
    openstack)
        SRC=$(mod_cache_dir "sigs.k8s.io/cluster-api-provider-openstack")
        # Copy the utils packages first (they are runtime deps of the types)
        copy_types "$SRC/pkg/utils/optional" "$DST_DIR/pkg/utils/optional"
        copy_types "$SRC/pkg/utils/errors"   "$DST_DIR/pkg/utils/errors"

        copy_types "$SRC/api/v1beta1" "$DST_DIR/api/v1beta1" \
            --allow "sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/"
        copy_types "$SRC/api/v1alpha1" "$DST_DIR/api/v1alpha1" \
            --allow "sigs.k8s.io/cluster-api-provider-openstack/api/" \
            --allow "sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/"
        ;;
    *)
        echo "ERROR: unknown provider: $PROVIDER" >&2
        exit 1
        ;;
esac

# Remove upstream test files and conversion machinery — HyperShift uses the
# copied types for CRD generation and controller references, not for CAPI's
# conversion webhooks or upstream tests.
find "$DST_DIR" \( -name '*_test.go' -o -name '*conversion*' \) -exec rm -f {} +

# Map provider names to their upstream module paths for controller-gen.
# controller-gen must resolve types from the workspace because each
# pkg/capi/<provider> is a separate Go module with a minimal go.mod.
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

MODULE_PATH=$(upstream_module "$PROVIDER")

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
