# Hacking

## Overview

What do I need to do to test...

* ...changes to `hypershift-operator`

  * [Install the HyperShift in development mode and run the operator locally](#how-to-run-the-hypershift-operator-in-a-local-process); or
  * [Install HyperShift using a custom image](#how-to-install-hypershift-with-a-custom-image)

* ...changes to `control-plane-operator` or any control plane operator

  * [Create a cluster using a custom image](#how-to-create-a-hypershift-guest-cluster-with-a-custom-image)

## Development How-To Guides

### How to run the HyperShift Operator in a local process

1. Ensure the `KUBECONFIG` environment variable points to a management cluster
   with no HyperShift installed yet.

2. Build HyperShift.

        # requires go v1.22+
        $ make build

3. Install HyperShift in development mode which causes the operator deployment
   to be deployment scaled to zero so that it doesn't conflict with your local
   operator process.

        $ bin/hypershift install --development

4. Run the HyperShift operator locally.

        $ bin/hypershift-operator run

### Building Custom Images
To build images that can be both used for HyperShift installation and Hosted Cluster creation

         docker build -f ./Dockerfile.dev --platform=linux/amd64 -t quay.io/blah/hypershift:${TAG} .
         docker push quay.io/blah/hypershift:${TAG}


To build only the HyperShift image for installation

         make IMG=quay.io/my/hypershift:latest docker-build docker-push


To build controlplane-operator image for Hosted Cluster creation

         docker build --platform linux/amd64 -t quay.io/blah/controlplaneoperator:<tag> -f Dockerfile.control-plane .
         docker push  docker push quay.io/blah/controlplaneoperator:<tag>


(Optional) If you need to build a release image containing changes from multiple pull requests you can do so by using Cluster Bot in slack.
   For example to build a 4.18 release image using the below PRs as examples
   * https://github.com/openshift/cluster-storage-operator/pull/522
   * https://github.com/openshift/hypershift/pull/4791

Run the following in Cluster Bot

    build 4.18,openshift/cluster-storage-operator#522,openshift/hypershift#4791

The bot will build a release image and link the job that created it, the image can be found at the bottom of the logs.


### How to install HyperShift with a custom image
1. Install HyperShift using the custom image:

        $ bin/hypershift install --hypershift-image quay.io/my/hypershift:latest

2. (Optional) If your repository is private, create a secret:

        oc create secret generic hypershift-operator-pull-secret  -n hypershift --from-file=.dockerconfig=/my/pull-secret --type=kubernetes.io/dockerconfig

   Then update the operator ServiceAccount in the hypershift namespace:

       oc patch serviceaccount operator -n hypershift -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'

### How to create a HyperShift Guest Cluster with a custom image
1. Create a guest cluster using the custom image:

        $ bin/hypershift create cluster openstack --release-image quay.io ...

### How to run the e2e tests

1. Complete [Prerequisites](https://hypershift-docs.netlify.app/getting-started/#prerequisites) with a public Route53
   Hosted Zone, for example with the following environment variables:

   ```shell
   BASE_DOMAIN="my.hypershift.dev"
   BUCKET_NAME="my-oidc-bucket"
   AWS_REGION="us-east-2"
   AWS_CREDS="my/aws-credentials"
   PULL_SECRET="/my/pull-secret"
   HYPERSHIFT_IMAGE="quay.io/my/hypershift:latest"
   ```

2. Install the HyperShift Operator on a cluster, filling in variables such as the S3 bucket name and region based on
   what was done in the prerequisites phase and potentially supplying a custom image.

   ```shell
   $ bin/hypershift install \
       --oidc-storage-provider-s3-bucket-name "${BUCKET_NAME}" \
       --oidc-storage-provider-s3-credentials "${AWS_CREDS}" \
       --oidc-storage-provider-s3-region "${AWS_REGION}" \
       --hypershift-image "${HYPERSHIFT_IMAGE}"
   ```

2. Run the tests.

   ```shell
   $ make e2e
   $ bin/test-e2e -test.v -test.timeout 0 \
       --e2e.aws-credentials-file "${AWS_CREDS}" \
       --e2e.pull-secret-file "${PULL_SECRET}" \
       --e2e.aws-region "${AWS_REGION}" \
       --e2e.availability-zones "${AWS_REGION}a,${AWS_REGION}b,${AWS_REGION}c" \
       --e2e.aws-oidc-s3-bucket-name "${BUCKET_NAME}" \
       --e2e.base-domain "${BASE_DOMAIN}"
   ```

### How to visualize the Go dependency graph

On MacOS, get a nice PDF of the graph:

```
brew install graphviz
go get golang.org/x/exp/cmd/modgraphviz
go mod graph | modgraphviz | dot -T pdf | open -a Preview.app -f
```

### How to update the HyperShift API CRDs

After making changes to types in the `api` package, make sure to update the
associated CRD files:

```shell
$ make api
```

### How to update third-party API types and CRDs

To update third-party API types (e.g. `sigs.k8s.io/cluster-api`), edit the dependency
version in `go.mod` and then update the contents of `vendor`:

```shell
$ go mod vendor
```

Then update the associated CRD files:

```shell
$ make api
```

### How to use go workspaces

Create a directory that will be the parent of the hypershift
code repository:

```shell
$ mkdir hypershift_ws
```

Under that directory, either move an existing hypershift repository or just clone hypershift again

```shell
$ cd hypershift_ws
$ git clone git@github.com:openshift/hypershift
```

Initialize the go workspace

```shell
$ go work init
$ go work use ./hypershift
$ go work use ./hypershift/api
$ go work sync
$ go work vendor
```

Now when running vscode, open the workspace directory to work with hypershift code.

Add hack for new pr task for copilot
