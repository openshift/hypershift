#!/bin/sh
# this script will attempt to run the passed in commands inside a container,
# its main purpose is for wrapping `make` commands when the local host does
# not have the appropriate development binaries. it should be used from the
# root of the project.
#
# example usage:
# ./hack/container-run.sh make test

set -ex

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
readonly SCRIPT_DIR

source "${SCRIPT_DIR}/utils.sh"

IMAGE=docker.io/openshift/origin-release:golang-1.16

ENGINE=$(get_container_engine)

ENGINE_CMD="${ENGINE} run --rm -v $(pwd):/go/src/github.com/openshift/hypershift:Z  -w /go/src/github.com/openshift/hypershift $IMAGE"

${ENGINE_CMD} $*
