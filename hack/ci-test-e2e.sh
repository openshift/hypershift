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

REQUEST_SERVING_COMPONENT_TEST="${REQUEST_SERVING_COMPONENT_TEST:-}"
REQUEST_SERVING_COMPONENT_PARAMS=""

if [[ -n "${REQUEST_SERVING_COMPONENT_TEST}" ]]; then
   REQUEST_SERVING_COMPONENT_PARAMS="--e2e.test-request-serving-isolation \
  --e2e.management-parent-kubeconfig=${MGMT_PARENT_KUBECONFIG} \
  --e2e.management-cluster-namespace=$(cat "${SHARED_DIR}/management_cluster_namespace") \
  --e2e.management-cluster-name=$(cat "${SHARED_DIR}/management_cluster_name")"
fi

declare -a default_args=(
  -test.v
  -test.run=${CI_TESTS_RUN}
  -test.parallel=20 \
  --e2e.aws-credentials-file=/etc/hypershift-pool-aws-credentials/credentials \
  --e2e.aws-zones=us-east-1a,us-east-1b,us-east-1c \
  --e2e.aws-oidc-s3-bucket-name=hypershift-ci-oidc \
  --e2e.aws-kms-key-alias=alias/hypershift-ci \
  --e2e.pull-secret-file=/etc/ci-pull-credentials/.dockerconfigjson \
  --e2e.base-domain=ci.hypershift.devcluster.openshift.com \
  --e2e.latest-release-image="${OCP_IMAGE_LATEST}" \
  --e2e.previous-release-image="${OCP_IMAGE_PREVIOUS}" \
  --e2e.disable-pki-reconciliation="${DISABLE_PKI_RECONCILIATION:=false}" \
  --e2e.additional-tags="expirationDate=$(date -d '4 hours' --iso=minutes --utc)" \
  --e2e.aws-endpoint-access=PublicAndPrivate \
  --e2e.external-dns-domain=service.ci.hypershift.devcluster.openshift.com \
  ${REQUEST_SERVING_COMPONENT_PARAMS}
)

# We would like all end-to-end testing for HyperShift to use this script, so we can set flags centrally
# and provide the jUnit results, etc, for everyone in the same way. In order to do that, we need to allow
# each consumer to pass disjoint sets of flags to the end-to-end binary. We already accept one argument,
# the set of tests to run, so we will continue to honor the previous calling convention unless the caller
# is passing more flags. That allows us to default to the current behavior and let callers opt into the
# new paradigm over time. Once that migration is done, default_args will be removed.
declare -a args="$@"
if [[ "$#" -lt 2 ]]; then
  args="${default_args[@]}"
fi

bin/test-e2e ${args} | tee /tmp/test_out &

wait $!
