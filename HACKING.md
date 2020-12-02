# Hacking

### Development workflow

Often it's easiest to develop the operator locally connected to a remote
cluster.

In this case, you might want to install with the development Kustomize
profile which uses 0 replicas for the operator deployment by default. This
makes it easy to iterate on the non-deployment manifests in conjunction
with the operator binary itself.

Starting from clean management cluster, run the following to get started: 

```bash
$ make build

$ make install PROFILE=development

$ make run-local
```

Or you might want to run your own image in the cluster to do integration
testing, in which case you may want to use the default (production) profile
and use `kubectl set image` (for example) to update the deployment.

### Visualizing dependencies

MacOS
```
brew install graphviz
go get golang.org/x/exp/cmd/modgraphviz
go mod graph | modgraphviz | dot -T pdf | open -a Preview.app -f
```
