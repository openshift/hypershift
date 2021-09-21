# Hacking

## Development How-to Guides

### How to run the HyperShift Operator in a local process

1. Ensure the `KUBECONFIG` environment variable points to a management cluster
   with no HyperShift installed yet.

2. Build HyperShift.

        # requires go v1.16+
        $ make build

3. Install HyperShift in development mode which causes the operator deployment
   to be deployment scaled to zero so that it doesn't conflict with your local
   operator process.

        $ bin/hypershift install --development

4. Run the HyperShift operator locally.

        $ bin/hypershift-operator run

### How to install HyperShift with a custom image

1. Build and push a custom image build to your own repository.

        make IMG=quay.io/my/hypershift:latest docker-build docker-push

2. Install HyperShift using the custom image:

        $ bin/hypershift install --hypershift-image quay.io/my/hypershift:latest

3. (Optional) If your repository is private, create a secret:

        oc create secret generic hypershift-operator-pull-secret  -n hypershift --from-file=.dockerconfig=/my/pull-secret --type=kubernetes.io/dockerconfig

Then update the operator ServiceAccount in the hypershift namespace:

       oc patch serviceaccount operator -n hypershift -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'

### How to run the e2e tests

1. Install HyperShift.
2. Run the tests.

   ```shell
        $ make e2e
        $ bin/test-e2e -test.v -test.timeout 0 \
          --e2e.aws-credentials-file /my/aws-credentials \
          --e2e.pull-secret-file /my/pull-secret \
          --e2e.base-domain my-basedomain
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
