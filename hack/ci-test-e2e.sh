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
}
trap generate_junit EXIT

# Filter out incompatible parallel flags for ginkgo
filtered_args=()
for arg in "$@"; do
  case "$arg" in
    -test.parallel=*|--test.parallel=*)
      # Skip parallel flags
      ;;
    *)
      filtered_args+=("$arg")
      ;;
  esac
done

bin/test-e2e "${filtered_args[@]}"| tee /tmp/test_out &

wait $!
