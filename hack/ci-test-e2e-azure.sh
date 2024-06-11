#!/usr/bin/env bash
#
# This script takes the first argument and use it as the input for -test.run.

set -euo pipefail

set -o monitor

set -x

generate_junit() {
  # propagate SIGTERM to the `test-e2e` process
  for child in $( jobs -p ); do
    kill "${child}"
  done
  # wait until `test-e2e` finishes gracefully
  wait

  cat  /tmp/test_out | go tool test2json -t > /tmp/test_out.json
  gotestsum --raw-command --junitfile="${ARTIFACT_DIR}/junit.xml" --format=standard-verbose -- cat /tmp/test_out.json
  # Ensure generated junit has a useful suite name
  sed -i 's/\(<testsuite.*\)name=""/\1 name="hypershift-e2e"/' "${ARTIFACT_DIR}/junit.xml"
}
trap generate_junit EXIT

bin/test-e2e \
  -test.v \
  -test.run='^TestCreateCluster.*|^TestNodePool.*' \
  -test.parallel=20 \
  --e2e.platform=Azure \
  --e2e.azure-credentials-file=/etc/hypershift-ci-jobs-azurecreds/credentials.json \
  --e2e.pull-secret-file=/etc/ci-pull-credentials/.dockerconfigjson \
  --e2e.base-domain=hypershift.azure.devcluster.openshift.com \
  --e2e.latest-release-image="${OCP_IMAGE_LATEST}" \
  --e2e.previous-release-image="${OCP_IMAGE_PREVIOUS}" \
  | tee /tmp/test_out &

wait $!
