# `kind`-Based Integration Testing

This test framework has a two-fold goal: provide a short iterating cycle for work on HyperShift operators locally
as well as a quick validation harness for features that do not require cloud-provider-specific functionality.

## Local Operation

The `run.sh` script allows for local iteration on HyperShift - use a shell to set up the environment and keep it
running, while using other shells to interact with the environment or even iterate on tests.

### Prerequisites

Make sure you have `kind` and some container image building utility (`docker`, `buildah`, `podman`) installed.
Keep an eye out for `too many open files` errors when launching `HostedCluster`s and apply the [remedy](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files).

An image is built by copying local binaries into a base container. Ensure the script knows how to map your
local operating system to a host container image, or you will have issues with dynamic linking.

Visit the [web console](https://console.redhat.com/openshift/create/local) to create a local pull secret -
this is required to interrogate OCP release bundles.

Set up the following environment variables:

```shell
export PATH="${PATH}:$(realpath ./bin)"
export WORK_DIR=/tmp/integration # this directory is persistent between runs, cleared only as necessary
export PULL_SECRET="REPLACE-ME"  # point this environment variable at the pull secret you generated
```

### Setup

Run the following in a shell - the process will set up the `kind` cluster and requisite `hypershift` infrastructure,
then wait for `SIGINT` indefinitely. On interrupt, the process will clean up after itself.

# TODO: add some mechanism to choose which tests the setup runs for
```shell
./test/integration/run.sh \
  cluster-up \ # start the kind cluster
  image \      # build the container image for HyperShift, load it into the cluster
  setup        # start the HyperShift operator and any HostedClusters for selected tests
```

### Tests

Run the following for quick iteration - the test process will expect that setup is complete. Use `${GO_TEST_FLAGS}` to
specify what subset of the tests to run.

```shell
./test/integration/run.sh \
  test # run tests
```

### Refreshing Image Content

When you've made changes to the HyperShift codebase and need to re-deploy the operators, run the following -
a new image will be built, loaded into the cluster, and all `Pod`s deploying the image will be deleted, so
the new image is picked up on restart.

```shell
./test/integration/run.sh \
  image \ # build the container image for HyperShift, load it into the cluster
  reload  # power-cycle all pods that should be running the image
```