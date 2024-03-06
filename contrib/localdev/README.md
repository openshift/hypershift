# Running a control plane locally with Podman and KinD

This README provides step-by-step instructions on how to run HyperShift binaries locally using Podman and KinD to test a control plane.
No images need to be built to run binaries for hypershift operator, control plane operator, and hosted cluster config operator.

## Prerequisites

* Podman
* Kind
* CLI tools: jq, oc
* Valid pull secret with access to CI releases in $HOME/.pull-secret

### Step 1: Install Podman (and make sure you have the latest version)

#### On MacOS you need to initialize and start a podman machine (ideally with 8GB of memory):

```bash
brew install podman
podman machine init --memory 8096
podman machine start
```

### Step 2: Install KinD

KinD (Kubernetes in Docker) runs local Kubernetes clusters using containers as nodes. You can install KinD by following these steps:

#### On MacOS

```bash
brew install kind
```

### Step 3: Create a Kubernetes Cluster with KinD

Now that you have both Podman and KinD installed, you can create a local Kubernetes cluster using KinD. Here's a basic example of creating a cluster:

```bash
KUBECONFIG=kind.kubeconfig kind create cluster
```

## Installing and Running HyperShift

### Step 1: Make your local binaries

```bash
make build
```

### Step 2: Install HyperShift on the Kind Cluster

You only need to do this once after initially creating the kind cluster. You can also run it every time you need to update CRDs

```
# Ensure you are using the KUBECONFIG for kind
export KUBECONFIG=kind.kubeconfig

# Install HyperShift
./contrib/localdev/install
```

### Step 3: Run the HyperShift operator

```
./contrib/localdev/run-operator
```

### Step 4: Create a HostedCluster

```
./contrib/localdev/create-cluster
```

Alternatively, you can render a hosted cluster and manually edit it before applying it to kubernetes

```
./contrib/localdev/render-cluster
```

The command above will generate a `cluster.yaml` file in the ./contrib/localdev directory

### Step 5: Run control plane components (CPO & HCCO)

The `start-cp` script runs other scripts like `start-cpo` and `start-hcco` to make sure your control plane components run

```
./contrib/localdev/start-cp
```

## Reviewing Logs

Logs for hypershift operator, control plane operator and hosted cluster config operator are placed in ./contrib/localdev:

* operator.log (hypershift operator log)
* cpo.log (control plane operator log)
* hcco.log (hosted cluster config operator log)

So for example, to follow the cpo logs, simply tail it:

```bash
tail -f ./contrib/localdev/cpo.log
```

## Destroying the HostedCluster

```bash
./contrib/localdev/delete-cluster
```

## Stopping Control Plane components

```
./contrib/localdev/stop-cp
```

Or alternatively, stop an individual component. To stop the cpo:

```
./contrib/localdev/stop-cpo
```


## Accessing the hosted cluster with the CLI

The `start-cp` script will result in a kubeconfig file named `./contrib/localdev/hosted.kubeconfig`. To access the hosted cluster, simply set that as your KUBECONFIG:

```bash
export KUBECONFIG=./contrib/localdev/hosted.kubeconfig
oc get clusteroperator -A
```