# Known Issues

## OLM default catalog sources in ImagePullBackOff state

When you work in a disconnected environment the OLM catalog sources will be still pointing to their original source, so all of these container images will keep it in ImagePullBackOff state even if the OLMCatalogPlacement is set to `Management` or `Guest`. From this point you have some options ahead:

1. Disable those OLM default catalog sources and using the oc-mirror binary, mirror the desired images into your private registry, creating a new Custom Catalog Source.
2. Mirror all the Container Images from all the catalog sources and apply an ImageContentSourcePolicy to request those images from the private registry.
3. Mirror all (or part of relying on the operator pruning option, see the image pruning section in https://docs.openshift.com/container-platform/4.14/installing/disconnected_install/installing-mirroring-disconnected.html#oc-mirror-updating-registry-about_installing-mirroring-disconnected ) the Container Images from all the catalog sources to the private registry and annotate the hostedCluster object with hypershift.openshift.io/olm-catalogs-is-registry-overrides annotation to get the ImageStream used for operator catalogs on your hosted-cluster pointing to your internal registry.

The most practical one is the first choice. To proceed with this option, you will need to follow [these instructions](https://docs.openshift.com/container-platform/4.14/installing/disconnected_install/installing-mirroring-disconnected.html). The process will make sure all the images get mirrored and also the ICSP will be generated properly.

Additionally when you're provisioning the HostedCluster you will need to add a flag to indicate that the OLMCatalogPlacement is set to `Guest` because if that's not set, you will not be able to disable them.

## Hypershift operator is failing to reconcile in Disconnected environments

If you are operating in a disconnected environment and have deployed the Hypershift operator, you may encounter an issue with the UWM telemetry writer. Essentially, it exposes Openshift deployment data in your RedHat account, but this functionality does not operate in a disconnected environments.

**Symptoms:**

- The Hypershift operator appears to be running correctly in the `hypershift` namespace but even if you creates the Hosted Cluster nothing happens.
- There will be a couple of log entries in the Hypershift operator:

```
{"level":"error","ts":"2023-12-20T15:23:01Z","msg":"Reconciler error","controller":"deployment","controllerGroup":"apps","controllerKind":"Deployment","Deployment":{"name":"operator","namespace":"hypershift"},"namespace":"hypershift","name":"operator","reconcileID":"451fde3c-eb1b-4cf0-98cb-ad0f8c6a6288","error":"cannot get telemeter client secret: Secret \"telemeter-client\" not found","stacktrace":"sigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).reconcileHandler\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:329\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).processNextWorkItem\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:266\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).Start.func2.2\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:227"}

{"level":"debug","ts":"2023-12-20T15:23:01Z","logger":"events","msg":"Failed to ensure UWM telemetry remote write: cannot get telemeter client secret: Secret \"telemeter-client\" not found","type":"Warning","object":{"kind":"Deployment","namespace":"hypershift","name":"operator","uid":"c6628a3c-a597-4e32-875a-f5704da2bdbb","apiVersion":"apps/v1","resourceVersion":"4091099"},"reason":"ReconcileError"}
```

**Solution:**

To resolve this issue, the solution will depend on how you deployed Hypershift:

- **The HO was deployed using ACM/MCE:** In this case you will need to create a ConfigMap in the `local-cluster` namespace (the namespace and ConfigMap name cannot be changed) called `hypershift-operator-install-flags` with this content:

```
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToRemove: --enable-uwm-telemetry-remote-write
```

- **The HO was deployed using the Hypershift binary:** In this case you will just need to remove the flag `--enable-uwm-telemetry-remote-write` from the hypershift deployment command.

## HCP CLI failing to create an hosted cluster with `failed to extract release metadata: failed to get repo setup: failed to create repository client for <your.internal.registry>

See: [CNV-38194](https://issues.redhat.com/browse/CNV-38194)
Currently the HCP client is directly (client side) trying to access the OCP release image, this implies some additional requirements on client side:

1. the HCP CLI client should be able to directly reach the internal mirror registry, and this could require additional configuration (a proxied access to the api server of the management server is not enough)
1. the HCP CLI client should explicitly consume the pull secret of the internal mirror registry
1. the HCP CLI client should explicitly consume the internal CA used to sign the TLS cert of the internal mirror registry

**Workaround:**
Explicitly set `--network-type OVNKubernetes` (or other valid SDN provider if needed) when running `hcp create cluster` to skip the auto-detection of the SDN network type.

## HCP: imageRegistryOverrides information is extracted only once on HyperShift operator initialization and never refreshed

See: [OCPBUGS-29110](https://issues.redhat.com/browse/OCPBUGS-29110)
According to https://hypershift-docs.netlify.app/how-to/disconnected/automatically-initialize-registry-overrides/ , the HyperShift Operator (HO) will automatically initialize the control plane operator (CPO) with any image registry override information from any ImageContentSourcePolicy (ICSP) or any ImageDigestMirrorSet (IDMS) instances from an OpenShift management cluster.
But due to a bug, the HyperShift Operator (HO) reads the image registry override information only during its startup and never refreshes them at runtime so ICSP and IDMS added after the initialization of the HyperShift Operator are going to get ignored.

**Workaround:**
Each time you add or amend and ICSP or an IDMS on your management cluster, you have to explicitly kill the HyperShift Operator pods:
```bash
$ oc delete pods -n hypershift -l name=operator
```

## HCP: hypershift-operator on disconnected clusters ignores ImageContentSourcePolicies when a ImageDigestMirrorSet exist on the management cluster

See: [OCPBUGS-29466](https://issues.redhat.com/browse/OCPBUGS-29466)

ICSP (deprecated) and IDMS can coexists on the management cluster but due to a bug if at least one IDMS object exists on the management cluster, the HyperShift Operator will completely ignore all the ICSP.

**Workaround:**
If you have at least an IDMS object, explicitly convert all the ICSPs to IDMSs:
```bash
$ mkdir -p /tmp/idms
$ mkdir -p /tmp/icsp
$ for i in $(oc get imageContentSourcePolicy -o name); do oc get ${i} -o yaml > /tmp/icsp/$(basename ${i}).yaml ; done
$ for f in /tmp/icsp/*; do oc adm migrate icsp ${f} --dest-dir /tmp/idms ; done
$oc apply -f /tmp/idms || true
```

## HCP: hypershift-operator on disconnected clusters ignores RegistryOverrides inspecting the control-plane-operator-image

See: [OCPBUGS-29494](https://issues.redhat.com/browse/OCPBUGS-29494)

**Symptoms:**
Creating an hosted cluster with:
hcp create cluster kubevirt --image-content-sources /home/mgmt_iscp.yaml  --additional-trust-bundle /etc/pki/ca-trust/source/anchors/registry.2.crt --name simone3 --node-pool-replicas 2 --memory 16Gi --cores 4 --root-volume-size 64 --namespace local-cluster --release-image virthost.ostest.test.metalkube.org:5000/localimages/local-release-image@sha256:66c6a46013cda0ad4e2291be3da432fdd03b4a47bf13067e0c7b91fb79eb4539 --pull-secret /tmp/.dockerconfigjson --generate-ssh

on the hostedCluster object we see:
```yaml
status:
conditions:
- lastTransitionTime: "2024-02-14T22:01:30Z"
  message: 'failed to look up image metadata for registry.ci.openshift.org/ocp/4.14-2024-02-14-135111@sha256:84c74cc05250d0e51fe115274cc67ffcf0a4ac86c831b7fea97e484e646072a6:
  failed to obtain root manifest for registry.ci.openshift.org/ocp/4.14-2024-02-14-135111@sha256:84c74cc05250d0e51fe115274cc67ffcf0a4ac86c831b7fea97e484e646072a6:
  unauthorized: authentication required'
  observedGeneration: 3
  reason: ReconciliationError
  status: "False"
  type: ReconciliationSucceeded
```

and in the logs of the hypershift operator:
```json
{"level":"info","ts":"2024-02-14T22:18:11Z","msg":"registry override coincidence not found","controller":"hostedcluster","controllerGroup":"hypershift.openshift.io","controllerKind":"HostedCluster","HostedCluster":{"name":"simone3","namespace":"local-cluster"},"namespace":"local-cluster","name":"simone3","reconcileID":"6d6a2479-3d54-42e3-9204-8d0ab1013745","image":"4.14-2024-02-14-135111"}
{"level":"error","ts":"2024-02-14T22:18:12Z","msg":"Reconciler error","controller":"hostedcluster","controllerGroup":"hypershift.openshift.io","controllerKind":"HostedCluster","HostedCluster":{"name":"simone3","namespace":"local-cluster"},"namespace":"local-cluster","name":"simone3","reconcileID":"6d6a2479-3d54-42e3-9204-8d0ab1013745","error":"failed to look up image metadata for registry.ci.openshift.org/ocp/4.14-2024-02-14-135111@sha256:84c74cc05250d0e51fe115274cc67ffcf0a4ac86c831b7fea97e484e646072a6: failed to obtain root manifest for registry.ci.openshift.org/ocp/4.14-2024-02-14-135111@sha256:84c74cc05250d0e51fe115274cc67ffcf0a4ac86c831b7fea97e484e646072a6: unauthorized: authentication required","stacktrace":"sigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).reconcileHandler\n\t/remote-source/app/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:326\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).processNextWorkItem\n\t/remote-source/app/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:273\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).Start.func2.2\n\t/remote-source/app/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:234"}
```

**Workaround:**
Explicitly use --annotations=hypershift.openshift.io/control-plane-operator-image=<controlPlaneOperatorImage> when creatng the hosted cluster.
```bash
# this will sync with the release image used on the management cluster, replace if needed
$ PAYLOADIMAGE=$(oc get clusterversion version -ojsonpath='{.status.desired.image}')
$ HO_OPERATOR_IMAGE="${PAYLOADIMAGE//@sha256:[^ ]*/@\$(oc adm release info -a /tmp/.dockerconfigjson "$PAYLOADIMAGE" | grep hypershift | awk '{print $2}')}"
$ hcp create cluster ... --annotations=hypershift.openshift.io/control-plane-operator-image=${HO_OPERATOR_IMAGE} ...
```

## (KubeVirt specific) oc-mirror does not mirror the RHCOS image for HyperShift KubeVirt provider from the OCP release payload

See: [OCPBUGS-29408](https://issues.redhat.com/browse/OCPBUGS-29408)

**Workaround:**
Explicitly mirror the RHCOS image for HyperShift KubeVirt provider.
This snippet assumes that `yq-v4` tool is already available on the bastion host.
```bash
mirror_registry=$(oc get imagecontentsourcepolicy -o json | jq -r '.items[].spec.repositoryDigestMirrors[0].mirrors[0]')
mirror_registry=${mirror_registry%%/*}
LOCALIMAGES=localimages
# this will sync with the release image used on the management cluster, replace if needed
PAYLOADIMAGE=$(oc get clusterversion version -ojsonpath='{.status.desired.image}')
oc image extract --file /release-manifests/0000_50_installer_coreos-bootimages.yaml ${PAYLOADIMAGE} --confirm
RHCOS_IMAGE=$(cat 0000_50_installer_coreos-bootimages.yaml | yq -r .data.stream | jq -r '.architectures.x86_64.images.kubevirt."digest-ref"')
RHCOS_IMAGE_NO_DIGEST=${RHCOS_IMAGE%@sha256*}
RHCOS_IMAGE_NAME=${RHCOS_IMAGE_NO_DIGEST##*/}
RHCOS_IMAGE_REPO=${RHCOS_IMAGE_NO_DIGEST%/*}
oc image mirror ${RHCOS_IMAGE} ${mirror_registry}/${LOCALIMAGES}/${RHCOS_IMAGE_NAME}
```

## HCP: recycler pods are not starting on hostedcontrolplane in disconnected environments ( ImagePullBackOff on quay.io/openshift/origin-tools:latest )

See: [OCPBUGS-31398](https://issues.redhat.com/browse/OCPBUGS-31398)

**Symptoms:**
Recycler pods are not starting on hosted control plane with ImagePullBackOff error on `quay.io/openshift/origin-tools:latest`.
The recycler pod template in the recycler-config config map on the hostedcontrolplane namespace refers to `quay.io/openshift/origin-tools:latest`:
```bash
$ oc get cm -n clusters-guest recycler-config -o json | jq -r '.data["recycler-pod.yaml"]' | grep "image"
image: quay.io/openshift/origin-tools:latest
```

**Workaround:**
Not available.
The recycler-config configmap is continuously reconciled by the control plane operator
pointing it back to `quay.io/openshift/origin-tools:latest` which will never be available for a disconnected cluster.

## HCP: imagesStreams on hosted-clusters pointing to image on private registries are failing due to tls verification, although the registry is correctly trusted

See: [OCPBUGS-31446](https://issues.redhat.com/browse/OCPBUGS-31446)
Probably a side effect of https://issues.redhat.com/browse/RFE-3093 - imagestream to trust CA added during the installation

**Symptoms:**
On the imageStream conditions on the the hosted cluster we see something like:
```yaml
tags:
- conditions:
    - generation: 2
      lastTransitionTime: "2024-03-27T12:43:56Z"
      message: 'Internal error occurred: virthost.ostest.test.metalkube.org:5000/localimages/local-test-image:e2e-7-registry-k8s-io-e2e-test-images-busybox-1-29-4-4zE9mRvED4RQoUxQ:
      Get "https://virthost.ostest.test.metalkube.org:5000/v2/": tls: failed to
      verify certificate: x509: certificate signed by unknown authority'
```
although the same image can be directly consumed by a pod on the same cluster

**Workaround:**
Patch all the image streams on the hostedcluster pointing to the internal registry setting insecure mode.
```bash
# KUBECONFIG points to the hosted cluster
IMAGESTREAMS=$(oc get imagestreams -n openshift -o=name)
for isName in ${IMAGESTREAMS}; do
  echo "#### Patching ${isName} using insecure registry"
  oc patch -n openshift ${isName} --type json -p '[{"op": "replace", "path": "/spec/tags/0/importPolicy/insecure", "value": true}]'
done
```

## IDMS/ICSP with only root registry are not propagated to the registry-override flag under ignition server

See: [OCPBUGS-33951](https://issues.redhat.com/browse/OCPBUGS-33951)

**Symptoms:**
Even having the ICSP/IDMS well set in the Management cluster and working fine in that side, the HostedControlPlane deployment is not capable of extract the metadata release from the OCP payload images. Something like this log entry will show up in the ControlPlaneOperator or HypershiftOperator pod.

```
failed to lookup release image: failed to extract release metadata: failed to get repo setup: failed to create repository client for https://registry.sample.net: Get "https://registry.sample.net/v2/": tls: failed to verify certificate: x509: certificate signed by unknown authority
```

**Root Cause**:
Once the ICSP/IDMS are created in the Management cluster and they are only using in the "sources" side a root registry instead of pointing a registry namespace, the image-overrides are not filled with the explicit destination OCP Metadata and Release images, so the Hypeshift operator cannot infer the exact location of the images in the private registry.

This is a sample:

```yaml
apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: image-policy
spec:
  imageDigestMirrors:
  - mirrors:
    - registry.sample.net/redhat.io
    source: registry.redhat.io
  - mirrors:
    - registry.sample.net/connect.redhat.com
    source: registry.connect.redhat.com
  - mirrors:
    - registry.sample.net/gcr.io
    source: gcr.io
  - mirrors:
    - registry.sample.net/docker.io
    source: docker.io
```

**Solution:**
In order to solve the issue and perform a successful HostedControlPlane disconnected deployment you need at least to create the OCP namespaced reference in a IDMS/ICSP. A sample will look like this:

```yaml
apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: ocp-release
spec:
  imageDigestMirrors:
  - mirrors:
    - registry.sample.net/quay.io/openshift-release-dev/ocp-v4.0-art-dev
    source: quay.io/openshift-release-dev/ocp-v4.0-art-dev
  - mirrors:
    - registry.sample.net/quay.io/openshift-release-dev/ocp-release
    source: quay.io/openshift-release-dev/ocp-release
```