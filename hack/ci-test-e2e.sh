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

bin/test-e2e "$@"| tee /tmp/test_out &

wait $!
