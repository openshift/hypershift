The Agent Service Config object is an essential component of the Assisted Service addon included in MCE/ACM, responsible for Baremetal cluster deployment. When the addon is enabled, you must deploy an operand (CRD) named `AgentServiceConfig` to configure it.

## Agent Service Config Objects

You can find the CRD described [here](https://github.com/openshift/assisted-service/blob/master/docs/operator.md#creating-an-agentserviceconfig-resource). In this context, we will focus on the main aspects to ensure its functionality in disconnected environments.

In addition to configuring the Agent Service Config, to ensure that Multicluster Engine functions properly in a disconnected environment, we need to include some additional ConfigMaps.

### Custom Registries Configuration

This ConfigMap contains the disconnected details necessary to customize the deployment.


```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: custom-registries
  namespace: multicluster-engine
  labels:
    app: assisted-service
data:
  ca-bundle.crt: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
  registries.conf: |
    unqualified-search-registries = ["registry.access.redhat.com", "docker.io"]

    [[registry]]
    prefix = ""
    location = "registry.redhat.io/openshift4"
    mirror-by-digest-only = true

    [[registry.mirror]]
      location = "registry.hypershiftbm.lab:5000/openshift4"

    [[registry]]
    prefix = ""
    location = "registry.redhat.io/rhacm2"
    mirror-by-digest-only = true
    ...
    ...
```

This object includes two fields:

1. **Custom CAs**: This field contains the Certificate Authorities (CAs) that will be loaded into the various processes of the deployment.
2. **Registries**: The `Registries.conf` field contains information about images and namespaces that need to be consumed from a mirror registry instead of the original source registry.

### Assisted Service Customization

The Assisted Service Customization ConfigMap is consumed by the Assisted Service operator and contains variables that modify the behavior of the controllers.


```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: assisted-service-config
  namespace: multicluster-engine
data:
  PUBLIC_CONTAINER_REGISTRIES: "quay.io,registry.ci.openshift.org,registry.redhat.io"
  ALLOW_CONVERGED_FLOW: "false"
```

You can find documentation on how to customize the operator [here](https://github.com/openshift/assisted-service/blob/master/docs/operator.md#specifying-environmental-variables-via-configmap).

### Assisted Service Config

The Assisted Service Config object includes the necessary information to ensure the correct functioning of the operator.

```yaml
---
apiVersion: agent-install.openshift.io/v1beta1
kind: AgentServiceConfig
metadata:
  annotations:
    unsupported.agent-install.openshift.io/assisted-service-configmap: assisted-service-config
  name: agent
  namespace: multicluster-engine
spec:
  mirrorRegistryRef:
    name: custom-registries
  databaseStorage:
    storageClassName: lvms-vg1
    accessModes:
    - ReadWriteOnce
    resources:
      requests:
        storage: 10Gi
  filesystemStorage:
    storageClassName: lvms-vg1
    accessModes:
    - ReadWriteOnce
    resources:
      requests:
        storage: 20Gi
  osImages:
  - cpuArchitecture: x86_64
    openshiftVersion: "4.14"
    rootFSUrl: http://registry.hypershiftbm.lab:8080/images/rhcos-414.92.202308281054-0-live-rootfs.x86_64.img
    url: http://registry.hypershiftbm.lab:8080/images/rhcos-414.92.202308281054-0-live.x86_64.iso
    version: 414.92.202308281054-0
```

In this section, we will emphasize the important aspects:

- The `metadata.annotations["unsupported.agent-install.openshift.io/assisted-service-configmap"]` Annotation references the ConfigMap name to be consumed by the operator for customizing behavior.
- The `spec.mirrorRegistryRef.name` points to the ConfigMap containing disconnected registry information to be consumed by the Assisted Service Operator. This ConfigMap injects these resources during the deployment process.
- The `spec.osImages` field contains different versions available for deployment by this operator. These fields are mandatory.

Let's fill in this section for the 4.14 dev preview ec4 version (sample):

- [Release URL](https://amd64.ocp.releases.ci.openshift.org/releasestream/4-dev-preview/release/4.14.0-ec.4)
- [RHCOS Info](https://releases-rhcos-art.apps.ocp-virt.prod.psi.redhat.com/?arch=x86_64&release=414.92.202307250657-0&stream=prod%2Fstreams%2F4.14-9.2#414.92.202307250657-0)

Assuming you've already downloaded the RootFS and LiveIso files:

```yaml
  - cpuArchitecture: x86_64
    openshiftVersion: "4.14"
    rootFSUrl: http://registry.hypershiftbm.lab:8080/images/rhcos-414.92.202309101331-0-live-rootfs.x86_64.img
    url: http://registry.hypershiftbm.lab:8080/images/rhcos-414.92.202309101331-0-live.x86_64.iso
    version: 414.92.202307250657-0
```

## Deployment

To deploy all these objects, simply concatenate them into a single file and apply them to the Management Cluster.

```bash
oc apply -f agentServiceConfig.yaml
```

This will trigger 2 pods

```bash
assisted-image-service-0                               1/1     Running   2             11d
assisted-service-668b49548-9m7xw                       2/2     Running   5             11d
```

!!! note

      The `Assisted Image Service` pod is responsible for creating the RHCOS Boot Image template, which will be customized for each cluster you deploy.

      The `Assisted Service` refers to the operator.
