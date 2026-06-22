# Templates of csi-snapshot-controller-operator objects.

Generated files in this directory come from https://github.com/openshift/cluster-csi-snapshot-controller-operator.

* To update the generated files, copy
  [`manifests/*-hypershift`](https://github.com/openshift/cluster-csi-snapshot-controller-operator/tree/master/manifests)
  from github.com/openshift/cluster-csi-snapshot-controller-operator to here, change `07_deployment-hypershift.yaml` name to `deployment.yaml` and change the container image from `quay.io/openshift/origin-cluster-csi-snapshot-controller-operator:latest` to `cluster-csi-snapshot-controller-operator`
* All changes in these files should be done in github.com/openshift/cluster-csi-snapshot-controller-operator repository,
  so we keep authoritative source of all csi-snapshot-controller-operator objects on a single place.
