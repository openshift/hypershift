# Dependency Management

We interface with some Kubernetes CRD/OpenAPI-based APIs whose Go types  are exposed indirectly through the Go modules of third-party applications. In these cases, declaring a Go module dependency on the applications to access the API types often leads to our application inheriting unwanted and conflicting transitive dependencies of the third-party application's internals.

In these cases, because working with the Go types instead of unstructured types is convenient, but because we can't leverage Go modules without unwanted dependency coupling, the workaround is to simply copy any such Go types into our tree.

All third-party API types which can't easily be declared as Go module dependencies are copied in their most minimal form to the `thirdparty` package and updated from their original sources manually as needed.


### How-to: Update Third-party API Go types

In all cases, any `_test.go` and conversion files can be ignored. All we need are the bare minimum types to speak over the wire to the apiserver.

For `sigs.k8s.io/cluster-api`:

1. Copy the relevant types from the [upstream api packages](https://github.com/kubernetes-sigs/cluster-api) to 
   to the local `thirdparty/clusterapi` package.
2. Adjust any import paths to reference `thirdparty` as necessary.

For `sigs.k8s.io/cluster-api-provider-aws`:

1. Copy the relevant types from the [upstream api packages](https://github.com/kubernetes-sigs/cluster-api-provider-aws) to 
   to the local `thirdparty/clusterapiprovideraws` package.
2. Adjust any import paths to reference `thirdparty` as necessary.


### How-to: Visualize the Go dependency graph

On MacOS, get a nice PDF of the graph:

```
brew install graphviz
go get golang.org/x/exp/cmd/modgraphviz
go mod graph | modgraphviz | dot -T pdf | open -a Preview.app -f
```
