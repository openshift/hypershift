#!/usr/bin/env bash
#
# This script takes the first argument and use it as the input for -test.run.

set -euo pipefail

set -o monitor

set -x

CI_TESTS_RUN=${1:-}
if [ -z  ${CI_TESTS_RUN} ]
then
      echo "Running all tests"
else
      echo "Running tests matching ${CI_TESTS_RUN}"
fi

generate_junit() {
  cat  /tmp/test_out | go tool test2json -t > /tmp/test_out.json
  gotestsum --raw-command --junitfile="${ARTIFACT_DIR}/junit.xml" --format=standard-verbose -- cat /tmp/test_out.json
}
trap generate_junit EXIT

bin/test-e2e \
  -test.v \
  -test.timeout=2h10m \
  -test.run=${CI_TESTS_RUN} \
  -test.parallel=20 \
  --e2e.aws-credentials-file=/etc/hypershift-pool-aws-credentials/credentials \
  --e2e.aws-zones=us-east-1a,us-east-1b,us-east-1c \
  --e2e.node-pool-replicas=1 \
  --e2e.pull-secret-file=/etc/ci-pull-credentials/.dockerconfigjson \
  --e2e.base-domain=ci.hypershift.devcluster.openshift.com \
  --e2e.latest-release-image="${OCP_IMAGE_LATEST}" \
  --e2e.previous-release-image="${OCP_IMAGE_PREVIOUS}" \
  --e2e.additional-tags="expirationDate=$(date -d '4 hours' --iso=minutes --utc)" \
  --e2e.aws-endpoint-access=PublicAndPrivate \
  --e2e.external-dns-domain=service.ci.hypershift.devcluster.openshift.com | tee /tmp/test_out
