#!/bin/bash

HYPERSHIFT_BIN=$1
CONTAINER_ENGINE=$(command -v podman 2>/dev/null || echo docker)

DIR=$(realpath $(dirname "${BASH_SOURCE[0]}"))
TMP_DIR=$DIR/tmp
mkdir -p $TMP_DIR

# generate hypershift operator manifests
${HYPERSHIFT_BIN} install --render \
	    --hypershift-image=quay.io/app-sre/hypershift-operator:latest \
		--namespace=hypershift \
		--oidc-storage-provider-s3-bucket-name="bucket" > $TMP_DIR/manifests.yaml

# process manifests into a template
cat $TMP_DIR/manifests.yaml | $CONTAINER_ENGINE run --rm -i \
    --entrypoint "/bin/bash" -v $DIR:/workdir:z \
    registry.access.redhat.com/ubi8/python-39 \
    -c "pip3.9 --disable-pip-version-check install -q pyyaml && /workdir/generate-saas-template.py" \
    > $DIR/saas_template.yaml

rm -rf $TMP_DIR
