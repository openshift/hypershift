# Hacking

## Development How-to Guides


### Run the operator in a local process

1. Ensure KUBECONFIG points to a management cluster with no HyperShift installed yet.

2. Build HyperShift.

        make build

3. Install HyperShift with the operator deployment scaled to zero so that it
   doesn't conflict with your local operator process. 

        make install PROFILE=development

4. Run the HyperShift operator locally.

        make run-local

### Run custom operator images

1. Build and push a custom image build to your own repository.

        make IMG=quay.io/my/hypershift:latest docker-build docker-push

2. Deploy the latest production version.

        make install PROFILE=production

3. Reconfigure the HyperShift operator deployment to use your custom image.
   This will also cause the image you specify to be used for the hosted cluster
   config operator as well.  

        oc --namespace hypershift set image deployment/operator operator=quay.io/my/hypershift:latest 

### Run the e2e tests

1. Install HyperShift.

        make install PROFILE=production

2. Run the tests.

        make test-e2e

### Visualize the Go dependency tree

MacOS
```
brew install graphviz
go get golang.org/x/exp/cmd/modgraphviz
go mod graph | modgraphviz | dot -T pdf | open -a Preview.app -f
```
