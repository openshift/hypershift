# Cluster Logging

HyperShift hosts OpenShift control planes from an OpenShift management cluster.

The hosted control plane is just a type of workload whose logs can be collected,
forward, or visualized no differently than any other workload run on a cluster by
a `cluster-admin`.

## Logging Topology

For simplicity, this example runs a converged log storage, visualization, and
collector topology.  In production settings, it is anticipated that each management
cluster will run with log collection enabled, but forward those logs to an off
cluster location for further analysis.

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
$ oc get clusterserviceversion -n openshift-logging
NAME                             DISPLAY                            VERSION    REPLACES                  PHASE
cluster-logging.5.0.4-21         Red Hat OpenShift Logging          5.0.4-21   cluster-logging.5.0.3-6   Succeeded
elasticsearch-operator.5.0.3-6   OpenShift Elasticsearch Operator   5.0.3-6                              Succeeded
```

To install Cluster Logging with a converged collector, storage, and visualization flow.

```shell
oc apply -f ./operator
```

Find the `kibana` route and login to the application with a user in `cluster-admin` role.

```shell
oc get routes -n openshift-logging
```

Login into the `kibana`, click `Management`, create `Index Pattern` with index name `app`, and use `@timestamp`
for *Time filter field name*.

Click `Discover`.

Input into search box the following query: "kubernetes.namespace_name:clusters*" to find all logs in namespaces with prefix clusters.

Edit the visualization to choose the columns to display, note `message` has the log body.