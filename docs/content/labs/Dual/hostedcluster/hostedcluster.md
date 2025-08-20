In this section, we will focus on all the related objects necessary to achieve a Disconnected Hosted Cluster deployment.

**Premises**:

- HostedCluster Name: `hosted-dual`
- HostedCluster Namespace: `clusters`
- Disconnected: `true`
- Network Stack: `Dual`

## Openshift Objects

### Namespaces

In a typical situation, the operator would be responsible for creating the HCP (HostedControlPlane) namespace. However, in this case, we want to include all the objects before the operator begins reconciliation over the HostedCluster object. This way, when the operator commences the reconciliation process, it will find all the objects in place.

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  creationTimestamp: null
  name: clusters-hosted-dual
spec: {}
status: {}
---
apiVersion: v1
kind: Namespace
metadata:
  creationTimestamp: null
  name: clusters
spec: {}
status: {}
```

!!! note

      We will **not** create objects one by one but will concatenate all of them in the same file and apply them with just one command.

### ConfigMap and Secrets

These are the ConfigMaps and Secrets that we will include in the HostedCluster deployment.

```yaml
---
apiVersion: v1
data:
  ca-bundle.crt: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
kind: ConfigMap
metadata:
  name: user-ca-bundle
  namespace: clusters
---
apiVersion: v1
data:
  .dockerconfigjson: xxxxxxxxx
kind: Secret
metadata:
  creationTimestamp: null
  name: hosted-dual-pull-secret
  namespace: clusters
---
apiVersion: v1
kind: Secret
metadata:
  name: sshkey-cluster-hosted-dual
  namespace: clusters
stringData:
  id_rsa.pub: ssh-rsa xxxxxxxxx
---
apiVersion: v1
data:
  key: nTPtVBEt03owkrKhIdmSW8jrWRxU57KO/fnZa8oaG0Y=
kind: Secret
metadata:
  creationTimestamp: null
  name: hosted-dual-etcd-encryption-key
  namespace: clusters
type: Opaque
```

### RBAC Roles

While not mandatory, it allows us to have the Assisted Service Agents located in the same HostedControlPlane namespace as the HostedControlPlane and still be managed by CAPI.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  creationTimestamp: null
  name: capi-provider-role
  namespace: clusters-hosted-dual
rules:
- apiGroups:
  - agent-install.openshift.io
  resources:
  - agents
  verbs:
  - '*'
```

### Hosted Cluster

This is a sample of the HostedCluster Object

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: hosted-dual
  namespace: clusters
spec:
  additionalTrustBundle:
    name: "user-ca-bundle"
  olmCatalogPlacement: guest
  imageContentSources:
  - source: quay.io/openshift-release-dev/ocp-v4.0-art-dev
    mirrors:
    - registry.hypershiftbm.lab:5000/openshift/release
  - source: quay.io/openshift-release-dev/ocp-release
    mirrors:
    - registry.hypershiftbm.lab:5000/openshift/release-images
  - mirrors:
  ...
  ...
  autoscaling: {}
  controllerAvailabilityPolicy: SingleReplica
  dns:
    baseDomain: hypershiftbm.lab
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 8Gi
        restoreSnapshotURL: null
        type: PersistentVolume
    managementType: Managed
  fips: false
  networking:
    clusterNetwork:
    - cidr: 10.132.0.0/14
    - cidr: fd01::/48
    networkType: OVNKubernetes
    serviceNetwork:
    - cidr: 172.31.0.0/16
    - cidr: fd02::/112
  platform:
    agent:
      agentNamespace: clusters-hosted-dual
    type: Agent
  pullSecret:
    name: hosted-dual-pull-secret
  release:
    image: registry.hypershiftbm.lab:5000/openshift/release-images:4.14.0-0.nightly-2023-08-29-102237
  secretEncryption:
    aescbc:
      activeKey:
        name: hosted-dual-etcd-encryption-key
    type: aescbc
  services:
  - service: APIServer
    servicePublishingStrategy:
      nodePort:
        address: api.hosted-dual.hypershiftbm.lab
      type: NodePort
  - service: OAuthServer
    servicePublishingStrategy:
      nodePort:
        address: api.hosted-dual.hypershiftbm.lab
      type: NodePort
  - service: OIDC
    servicePublishingStrategy:
      nodePort:
        address: api.hosted-dual.hypershiftbm.lab
      type: NodePort
  - service: Konnectivity
    servicePublishingStrategy:
      nodePort:
        address: api.hosted-dual.hypershiftbm.lab
      type: NodePort
  - service: Ignition
    servicePublishingStrategy:
      nodePort:
        address: api.hosted-dual.hypershiftbm.lab
      type: NodePort
  sshKey:
    name: sshkey-cluster-hosted-dual
status:
  controlPlaneEndpoint:
    host: ""
    port: 0
```

!!! note

    The `imageContentSources` section within the `spec` field contains mirror references for user workloads within the HostedCluster.

As you can see, all the objects created before are referenced here. You can also refer to the [documentation](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io%2fv1beta1) where all the fields are described.

## Deployment

To deploy these objects, simply concatenate them into the same file and apply them against the management cluster:

```bash
oc apply -f 01-4.14-hosted_cluster-nodeport.yaml
```

This will raise up a functional Hosted Control Plane.

``` yaml
NAME                                                  READY   STATUS    RESTARTS   AGE
capi-provider-5b57dbd6d5-pxlqc                        1/1     Running   0   	   3m57s
catalog-operator-9694884dd-m7zzv                      2/2     Running   0          93s
cluster-api-f98b9467c-9hfrq                           1/1     Running   0   	   3m57s
cluster-autoscaler-d7f95dd5-d8m5d                     1/1     Running   0          93s
cluster-image-registry-operator-5ff5944b4b-648ht      1/2     Running   0          93s
cluster-network-operator-77b896ddc-wpkq8              1/1     Running   0          94s
cluster-node-tuning-operator-84956cd484-4hfgf         1/1     Running   0          94s
cluster-policy-controller-5fd8595d97-rhbwf            1/1     Running   0          95s
cluster-storage-operator-54dcf584b5-xrnts             1/1     Running   0          93s
cluster-version-operator-9c554b999-l22s7              1/1     Running   0          95s
control-plane-operator-6fdc9c569-t7hr4                1/1     Running   0 	       3m57s
csi-snapshot-controller-785c6dc77c-8ljmr              1/1     Running   0 	       77s
csi-snapshot-controller-operator-7c6674bc5b-d9dtp     1/1     Running   0 	       93s
dns-operator-6874b577f-9tc6b                          1/1     Running   0          94s
etcd-0                                                3/3     Running   0 	       3m39s
hosted-cluster-config-operator-f5cf5c464-4nmbh        1/1     Running   0 	       93s
ignition-server-6b689748fc-zdqzk                      1/1     Running   0          95s
ignition-server-proxy-54d4bb9b9b-6zkg7                1/1     Running   0 	       95s
ingress-operator-6548dc758b-f9gtg                     1/2     Running   0          94s
konnectivity-agent-7767cdc6f5-tw782                   1/1     Running   0 	       95s
kube-apiserver-7b5799b6c8-9f5bp                       4/4     Running   0 	       3m7s
kube-controller-manager-5465bc4dd6-zpdlk              1/1     Running   0 	       44s
kube-scheduler-5dd5f78b94-bbbck                       1/1     Running   0 	       2m36s
machine-approver-846c69f56-jxvfr                      1/1     Running   0          92s
oauth-openshift-79c7bf44bf-j975g                      2/2     Running   0 	       62s
olm-operator-767f9584c-4lcl2                          2/2     Running   0 	       93s
openshift-apiserver-5d469778c6-pl8tj                  3/3     Running   0 	       2m36s
openshift-controller-manager-6475fdff58-hl4f7         1/1     Running   0 	       95s
openshift-oauth-apiserver-dbbc5cc5f-98574             2/2     Running   0          95s
openshift-route-controller-manager-5f6997b48f-s9vdc   1/1     Running   0          95s
packageserver-67c87d4d4f-kl7qh                        2/2     Running   0          93s
```


And this is how the HostedCluster looks like:

```bash
NAMESPACE   NAME         VERSION   KUBECONFIG                PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
clusters    hosted-dual            hosted-admin-kubeconfig   Partial    True 	      False         The hosted control plane is available
```

After some time, we will have almost all the pieces in place, and the Control Plane operator awaits for the worker nodes to join the cluster. To achieve this, we need to create some more objects. Let's discuss the `InfraEnv` and the `BareMetalHost` in the following sections.