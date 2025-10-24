#!/usr/bin/env bash


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
  bin/junit-post-process "${ARTIFACT_DIR}/junit.xml" > "${ARTIFACT_DIR}/junit-post-processed.xml"
  mv "${ARTIFACT_DIR}/junit-post-processed.xml" "${ARTIFACT_DIR}/junit.xml"
}
trap generate_junit EXIT

bin/test-e2e "$@"| tee /tmp/test_out &

wait $!
