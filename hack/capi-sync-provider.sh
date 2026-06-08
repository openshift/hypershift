#!/usr/bin/env bash
set -euo pipefail

PROVIDER="${1:?usage: capi-sync-provider.sh <provider>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOD_DIR="$ROOT/hack/capi-vendor/$PROVIDER"
DST_DIR="$ROOT/pkg/capi/$PROVIDER"
COPY_TOOL="$ROOT/hack/copy-capi-types/main.go"
BOILERPLATE="$ROOT/hack/boilerplate.go.txt"
CONTROLLER_GEN="${CONTROLLER_GEN:-controller-gen}"

run_go() {
    GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go "$@"
}

# Download a module into the cache and print its directory.
mod_cache_dir() {
    local module="$1"
    (cd "$MOD_DIR" && GO111MODULE=on GOWORK=off GOFLAGS= go mod download -json "$module") | \
        sed -n 's/.*"Dir": *"\(.*\)".*/\1/p'
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
        find "$DST_DIR" \( -name 'rosa*' -o -name 'awsfargate*' -o -name 'awsmanaged*' \) -exec rm -f {} +
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
    esac
}

echo "Running controller-gen for deepcopy..."
MODULE_PATH=$(upstream_module "$PROVIDER")
cd "$ROOT/hack/workspace"
GOWORK="$ROOT/hack/workspace/go.work" GOFLAGS= \
    "$CONTROLLER_GEN" object:headerFile="$BOILERPLATE" paths="$MODULE_PATH/..."

echo "Done syncing $PROVIDER"
