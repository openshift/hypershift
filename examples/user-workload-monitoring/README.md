# Monitoring

HyperShift hosts OpenShift control planes from an OpenShift management cluster.

The hosted control plane is just a type of workload that can be monitored via
OpenShift user workload monitoring.

## Topology

For simplicity, this example enables user workload monitoring on an OpenShift
cluster using defaults.  Once enabled, the hosted control plane component
metrics are visible in Thanos querier.

## How to install

**Prerequisites:**

* Admin access to an OpenShift cluster (version 4.7+) specified by the `KUBECONFIG` environment variable
* The `hypershift` operator is installed on the target cluster.

To install prerequisite operators for ElasticSearch and Cluster Logging:

```shell
oc apply -f ./manifests
```

Ensure all prereqs are succeeded.

```shell
$ oc get pods -n openshift-user-workload-monitoring
```

As `cluster-admin` visit the Thanos Querier route.

```shell
$ oc get routes -n openshift-monitoring thanos-querier
```

Execute following queries:

Is HyperShift up?

```shell
up{namespace="hypershift"}
```

Is any HyperShift controller having a reconcile error?

```shell
controller_runtime_reconcile_errors_total{namespace="hypershift"}
```

TODO: add more queries