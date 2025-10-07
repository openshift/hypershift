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

# Check if running on AWS platform and create required StorageClass for driver-config tests
# Parse the platform from the command line arguments
PLATFORM=""
for arg in "$@"; do
  if [[ "$arg" == "--e2e.platform=aws" ]] || [[ "$arg" == "--e2e.platform=AWS" ]]; then
    PLATFORM="aws"
    break
  fi
done

if [[ "$PLATFORM" == "aws" ]]; then
  echo "Detected AWS platform, creating a-gp3-csi StorageClass for driver-config tests..."

  # Create the StorageClass using kubectl
  cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: a-gp3-csi
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  encrypted: "true"
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
EOF

  echo "StorageClass a-gp3-csi created successfully"
fi

bin/test-e2e "$@"| tee /tmp/test_out &

wait $!
