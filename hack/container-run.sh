#!/bin/sh
# this script will attempt to run the passed in commands inside a container,
# its main purpose is for wrapping `make` commands when the local host does
# not have the appropriate development binaries. it should be used from the
# root of the project.
#
# example usage:
# ./hack/container-run.sh make test

set -ex

if command -v podman > /dev/null 2>&1
then
    ENGINE=podman
elif command -v docker > /dev/null 2>&1
then
    ENGINE=docker
else
    echo "No container runtime found"
    exit 1
fi

IMAGE=docker.io/openshift/origin-release:golang-1.16

ENGINE_CMD="${ENGINE} run --rm -v $(pwd):/go/src/github.com/openshift/hypershift:Z  -w /go/src/github.com/openshift/hypershift $IMAGE"

${ENGINE_CMD} $*
