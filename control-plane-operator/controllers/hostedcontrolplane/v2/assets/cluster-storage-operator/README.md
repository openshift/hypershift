# Templates of cluster-storage-operator objects.

Generated files in this directory with "hypershift" name come from https://github.com/openshift/cluster-storage-operator.

* To update the generated file, copy
  [`manifests/10_deployment-hypershift.yaml`](https://github.com/openshift/cluster-storage-operator/blob/master/manifests/10_deployment-hypershift.yaml)
  from github.com/openshift/cluster-storage-operator to `deployment.yaml` and change the container image from `quay.io/openshift/origin-cluster-storage-operator:latest` to `cluster-storage-operator`
* All changes in these files should be done in github.com/openshift/cluster-storage-operator repository,
  so we keep authoritative source of all cluster-storage-operator objects on a single place.

Role and rolebinding is custom for hypershift only.
