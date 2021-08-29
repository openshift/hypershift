# Registry Client ReleaseInfo Provider Test

The purpose of this test is to validate that the registryclient provider will not cause excessive memory usage during normal controller operation even when streaming very large images.

## Test Setup

Build the two Dockerfiles in this directory using the root of the hypershift directory as context, push them to a registry, and then launch the pod that will run the test.

```sh
CONTAINER_CLIENT=podman  # Can be docker

TEST_IMAGE_REF=quay.io/myns/registryclient_test:test   # Use your own registry and namespace here
LARGE_IMAGE_REF=quay.io/myns/registryclient_test:large # Use your own registry and namespace here

$CONTAINER_CLIENT build -t "${TEST_IMAGE_REF}" -f releaseinfo/registryclient/test/Dockerfile.testdriver .
$CONTAINER_CLIENT build -t "${LARGE_IMAGE_REF}" -f releaseinfo/registryclient/test/Dockerfile.large .
$CONTAINER_CLIENT push "${TEST_IMAGE_REF}"
$CONTAINER_CLIENT push "${LARGE_IMAGE_REF}"

oc project registryclient-test || oc new-project registryclient-test
oc process -f ./releaseinfo/registryclient/test/testpod-template.yaml --local \
   --param TEST_CONTAINER_IMAGE="${TEST_IMAGE_REF}" \
   --param TEST_LARGE_IMAGE="${LARGE_IMAGE_REF}" -o yaml | oc apply -f -
```

If the created pod is successful, then the test has passed.
