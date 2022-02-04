# Restart Control Plane Components

The `hypershift.openshift.io/restart-date` annotation allows a hypershift administrator to restart all control plane components for a particular `HostedCluster`. This is useful for certificate rotation and in various development situations where a control plane restart is desired.

Restart a control plane by annotating the `HostedCluster` resource:

```
oc annotate hostedcluster -n clusters example hypershift.openshift.io/restart-date=$(date --iso-8601=seconds)
```

The restart occurs whenever the value of this annotation changes. The `date` command in the example serves only as a source of a unique string.  The annotation is treated only as a string, not a timestamp.

The list of components restarted are listed below:

* catalog-operator
* certified-operators-catalog
* cluster-api
* cluster-autoscaler
* cluster-policy-controller
* cluster-version-operator
* community-operators-catalog
* control-plane-operator
* hosted-cluster-config-operator
* ignition-server
* ingress-operator
* konnectivity-agent
* konnectivity-server
* kube-apiserver
* kube-controller-manager
* kube-scheduler
* machine-approver
* oauth-openshift
* olm-operator
* openshift-apiserver
* openshift-controller-manager
* openshift-oauth-apiserver
* packageserver
* redhat-marketplace-catalog
* redhat-operators-catalog