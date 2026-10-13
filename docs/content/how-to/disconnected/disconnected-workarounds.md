# Disconnected workarounds

## Make ImageContentSourcePolicies work using image tags
The ImageContentSourcePolicies (ICSP) have not exposed the API to handle the parameter `mirror-by-digest-only`, so in order to do that, we need to manually create a MachineConfig change to be applied in all the HostedCluster workers. This change will perform a similar action in the workers like an ICSP.

!!! note

    You don't need to do this with future versions of Openshift because `config.openshift.io/v1` has an exposed API to do this, called `ImageTagMirrorSet`.

!!! note

    This workaround could also be applied in the management cluster first, prior to deploying a HostedCluster from the management cluster.

This is our MachineConfig template:

- `mc-icsp-template.yaml`
```yaml
---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: 99-worker-mirror-by-digest-registries
spec:
  config:
    ignition:
      version: 3.1.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,$B64_RAWICSP
        filesystem: root
        mode: 420
        path: /etc/containers/registries.conf.d/99-mirror-by-digest-registries.conf
```

Basically, we create a file inside of `/etc/containers/registries.conf.d/` called `99-mirror-by-digest-registries.conf` which tells the runtime to use the custom registry instead of the external one.

Also, here we have our final file content:

- `icsp-raw-mc.yaml`
```ini
[[registry]]
  prefix = ""
  location = "registry.redhat.io/openshift4/ose-kube-rbac-proxy"
  mirror-by-digest-only = false

[[registry.mirror]]
location = "registry.ocp-edge-cluster-0.qe.lab.redhat.com:5000/openshift4/ose-kube-rbac-proxy"

[[registry]]
  prefix = ""
  location = "quay.io/acm-d"
  mirror-by-digest-only = false

[[registry.mirror]]
location = "registry.ocp-edge-cluster-0.qe.lab.redhat.com:5000/acm-d"

[[registry]]
  prefix = ""
  location = "quay.io/open-cluster-management/addon-manager"
  mirror-by-digest-only = false

[[registry.mirror]]
location = "registry.ocp-edge-cluster-0.qe.lab.redhat.com:5000/open-cluster-management/addon-manager"
```

Now we just need to mix the things up and apply them into our HostedCluster

```bash
export B64_RAWICSP=$(cat icsp-raw-mc.yaml | base64)
envsubst < mc-icsp-template.yaml | oc apply -f -
```

These two commands will create the MachineConfig change in the Openshift cluster, so eventually the worker nodes will get rebooted.

After applying this change, the worker nodes will be able to consume the mirror when only the tags are involved.