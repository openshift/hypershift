The mirroring step can take some time to complete, so we recommend starting with this part once the Registry server is up and running.

For this purpose, we will use the `oc-mirror` tool, a binary that utilizes an object called `ImageSetConfiguration`.

In this file, you can specify:

- The OpenShift versions to mirror (they should be located in quay.io).
- The additional operators to mirror, selecting packages individually.
- The extra images you want to add to the repository.

!!! note

    Please ensure you modify the appropriate fields to align with your laboratory environment.

Here is an example of the `ImageSetConfiguration` that we will use for our mirroring:

```yaml
apiVersion: mirror.openshift.io/v1alpha2
kind: ImageSetConfiguration
storageConfig:
  registry:
    imageURL: registry.hypershiftbm.lab:5000/openshift/release/metadata:latest
mirror:
  platform:
    channels:
    - name: candidate-4.14
      minVersion: 4.14.0-ec.1
      maxVersion: 4.14.0-ec.3
      type: ocp
    graph: true
  additionalImages:
  - name: quay.io/karmab/origin-keepalived-ipfailover:latest
  - name: quay.io/karmab/kubectl:latest
  - name: quay.io/karmab/haproxy:latest
  - name: quay.io/karmab/mdns-publisher:latest
  - name: quay.io/karmab/origin-coredns:latest
  - name: quay.io/karmab/curl:latest
  - name: quay.io/karmab/kcli:latest
  - name: quay.io/mavazque/trbsht:latest
  - name: quay.io/jparrill/hypershift:BMSelfManage-v4.14-rc-v3
  - name: registry.redhat.io/openshift4/ose-kube-rbac-proxy:v4.10
  operators:
  - catalog: registry.redhat.io/redhat/redhat-operator-index:v4.13
    packages:
    - name: lvms-operator
    - name: local-storage-operator
    - name: odf-csi-addons-operator
    - name: odf-operator
    - name: mcg-operator
    - name: ocs-operator
    - name: metallb-operator
```

Make sure you have your `${HOME}/.docker/config.json` file updated with the registries you are trying to mirror from and your private registry to push the images to.

After that, we can begin the mirroring process:

```bash
oc-mirror --source-skip-tls --config imagesetconfig.yaml docker://${REGISTRY}
```

Once the mirror finishes, you will have a new folder called `oc-mirror-workspace/results-XXXXXX/` which contains the ICSP and the CatalogSources to be applied later on the HostedCluster.

## Mirroring Nightly and CI releases

The bad part in all of this, we cannot cover nightly or CI versions of Openshift so we will need to use the `oc adm release mirror` to mirror those versions.

To mirror the nightly versions we need for this deployment, you need to execute this:

```bash
REGISTRY=registry.$(hostname --long):5000

oc adm release mirror \
  --from=registry.ci.openshift.org/ocp/release:4.14.0-0.nightly-2023-08-29-102237 \
  --to=${REGISTRY}/openshift/release \
  --to-release-image=${REGISTRY}/openshift/release-images:4.14.0-0.nightly-2023-08-29-102237
```

For more detailed and updated information, you can visit the official [Documentation](https://docs.openshift.com/container-platform/4.13/installing/disconnected_install/installing-mirroring-disconnected.html) or [GitHub repository](https://github.com/openshift/oc-mirror)

## Mirror MCE internal releases

In order to mirror all the MCE latest images uploaded to quay.io or if it's internal and you can access the ACM documentation.

- [Red Hat Official Documentation](https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.8/html/clusters/cluster_mce_overview#install-on-disconnected-networks)
- Red Hat Internal deployment [Brew Registry deployment](https://github.com/stolostron/deploy/blob/master/docs/deploy-from-brew.md)
