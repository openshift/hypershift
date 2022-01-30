# Restart Control Plane Components with Restart Annotation

The Restart Annotation allows a user to trigger a rolling restart of all control-plane components in the management cluster. This is helpful in certain situations like when you want to force a reload of all the certificates components are using in the control plane. It is also a helpful operational debug step to see if a restart resolves any abnormalities being experienced in the control plane components.

To utilize: the restart annotation is passed to the Hostedcluster object

```
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  name: example
  namespace: master
  annotations:
    hypershift.openshift.io/restart-date: "2022-01-31T12:56:54+0000"
....
```

When that annotation is passed: every component in the clusters control plane will proceed to do a rolling restart. This will occur anytime a new value is passed to the restart date annotation.

The list of components restarted are listed below:
- catalog-operator
- certified-operators-catalog
- cluster-api
- cluster-autoscaler
- cluster-policy-controller
- cluster-version-operator
- community-operators-catalog
- control-plane-operator
- hosted-cluster-config-operator
- ignition-server
- ingress-operator
- konnectivity-agent
- konnectivity-server
- kube-apiserver
- kube-controller-manager
- kube-scheduler
- machine-approver
- oauth-openshift
- olm-operator
- openshift-apiserver
- openshift-controller-manager
- openshift-oauth-apiserver
- packageserver
- redhat-marketplace-catalog
- redhat-operators-catalog