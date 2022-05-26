#!/bin/bash
# This script uses `ko` to build a component image and then publish it to an
# OCP image registry discovered by `oc registry info`. If successful, the script
# outputs a single line which is the internal pullspec of the published image
# suitable for reference by pods in the OCP cluster.
#
# This script accepts a single argument which is the relative path to a component's
# main package directory containing `main.go`.

set -euo pipefail

INTERNAL_REPO="image-registry.openshift-image-registry.svc:5000"

PROJECT="$1"

export KO_DOCKER_REPO="$(oc registry info --public)/hypershift"
EXTERNAL_PULLSPEC=$(ko publish --insecure-registry "$PROJECT")

INTERNAL_PULLSPEC="$INTERNAL_REPO/$(echo $EXTERNAL_PULLSPEC | cut -d'/' -f2-3)"
echo $INTERNAL_PULLSPEC
