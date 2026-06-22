# Configuring KubeVirt VMs with JSON Patches

HyperShift provides a JSON patch mechanism that allows advanced customization of
the KubeVirt VirtualMachine template. This is useful when you need to configure VM
properties that are not directly exposed through the NodePool API, such as node
affinity, tolerations, or additional network interfaces.

## Overview

The `hypershift.openshift.io/kubevirt-vm-jsonpatch` annotation accepts a JSON
array of [RFC 6902 JSON Patch](https://datatracker.ietf.org/doc/html/rfc6902)
operations. The annotation can be set on either the `HostedCluster` or the
`NodePool` resource (or both). When set on both, the `HostedCluster` patches are
applied first and the `NodePool` patches are applied second, meaning NodePool
patches take precedence for the same path.

Each patch operation is an object with the following fields:

| Field   | Description                                                         | Required                       |
|---------|---------------------------------------------------------------------|--------------------------------|
| `op`    | The operation to perform: `add`, `replace`, `remove`, `move`, `copy`, `test` | Yes                            |
| `path`  | A JSON Pointer path into the `VirtualMachineTemplateSpec`           | Yes                            |
| `from`  | Source JSON Pointer path (used by `move` and `copy`)                | Yes (`move`, `copy`)           |
| `value` | The value to use for the operation                                  | Yes (`add`, `replace`, `test`) |

The `path` field targets the
[VirtualMachineTemplateSpec](https://kubevirt.io/api-reference/) structure. For
example, the path `/spec/template/spec/affinity` refers to the VM instance's
affinity configuration.

!!! note

    HyperShift's `add` operation automatically creates intermediate path elements
    if they do not exist. This behavior differs from RFC 6902, which requires
    parent paths to exist. You can add deeply nested fields without ensuring
    parent objects are present; this convenience is specific to HyperShift's
    implementation (`EnsurePathExistsOnAdd` is enabled in the underlying
    `evanphx/json-patch` library) and may not be portable to other JSON Patch
    tools.

## Configuring Node Affinity

By default, HyperShift configures either `PodAntiAffinity` or
`TopologySpreadConstraints` on KubeVirt VMs to spread them across nodes. However,
the NodePool API does not expose a `NodeAffinity` field. To schedule VMs on
specific nodes based on labels, you can use the JSON patch annotation to add node
affinity rules.

!!! note

    When adding node affinity, use the `add` operation on the
    `/spec/template/spec/affinity/nodeAffinity` sub-path rather than replacing
    the entire `/spec/template/spec/affinity` object. Replacing the full affinity
    object would remove the default pod anti-affinity or topology spread
    constraints that HyperShift sets to distribute VMs across nodes.

### Required Node Affinity

The following example schedules VMs only on infrastructure nodes labeled with
`node-type=kubevirt-worker`. This uses `requiredDuringSchedulingIgnoredDuringExecution`
to enforce strict placement.

```yaml linenums="1"
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: example
  namespace: clusters
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "add",
          "path": "/spec/template/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution",
          "value": {
            "nodeSelectorTerms": [
              {
                "matchExpressions": [
                  {
                    "key": "node-type",
                    "operator": "In",
                    "values": ["kubevirt-worker"]
                  }
                ]
              }
            ]
          }
        }
      ]
spec:
  clusterName: example
  replicas: 2
  platform:
    kubevirt:
      compute:
        cores: 4
        memory: 8Gi
      rootVolume:
        persistent:
          size: 32Gi
        type: Persistent
    type: KubeVirt
```

You can also apply the annotation to an existing NodePool using `oc annotate`:

```shell linenums="1"
oc annotate nodepool -n clusters example \
  hypershift.openshift.io/kubevirt-vm-jsonpatch='[{"op":"add","path":"/spec/template/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution","value":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"node-type","operator":"In","values":["kubevirt-worker"]}]}]}}]'
```

### Preferred Node Affinity

The following example uses `preferredDuringSchedulingIgnoredDuringExecution` to
express a preference for nodes with the label `gpu=true`, without strictly
requiring it. The `weight` field (1-100) determines the priority of this
preference relative to other scheduling constraints.

```yaml linenums="1"
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: example
  namespace: clusters
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "add",
          "path": "/spec/template/spec/affinity/nodeAffinity/preferredDuringSchedulingIgnoredDuringExecution",
          "value": [
            {
              "weight": 100,
              "preference": {
                "matchExpressions": [
                  {
                    "key": "gpu",
                    "operator": "In",
                    "values": ["true"]
                  }
                ]
              }
            }
          ]
        }
      ]
spec:
  clusterName: example
  replicas: 2
  platform:
    kubevirt:
      compute:
        cores: 4
        memory: 8Gi
      rootVolume:
        persistent:
          size: 32Gi
        type: Persistent
    type: KubeVirt
```

### Combining Required and Preferred Affinity

You can combine both required and preferred node affinity rules in a single
annotation by including multiple patch operations:

```yaml linenums="1"
metadata:
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "add",
          "path": "/spec/template/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution",
          "value": {
            "nodeSelectorTerms": [
              {
                "matchExpressions": [
                  {
                    "key": "node-role",
                    "operator": "In",
                    "values": ["compute"]
                  }
                ]
              }
            ]
          }
        },
        {
          "op": "add",
          "path": "/spec/template/spec/affinity/nodeAffinity/preferredDuringSchedulingIgnoredDuringExecution",
          "value": [
            {
              "weight": 50,
              "preference": {
                "matchExpressions": [
                  {
                    "key": "zone",
                    "operator": "In",
                    "values": ["us-east-1a"]
                  }
                ]
              }
            }
          ]
        }
      ]
```

## Applying Patches at the HostedCluster Level

When the same node affinity rule should apply to all NodePools, you can set the
annotation on the `HostedCluster` resource instead of each individual NodePool:

```yaml linenums="1"
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "add",
          "path": "/spec/template/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution",
          "value": {
            "nodeSelectorTerms": [
              {
                "matchExpressions": [
                  {
                    "key": "node-type",
                    "operator": "In",
                    "values": ["kubevirt-worker"]
                  }
                ]
              }
            ]
          }
        }
      ]
spec:
  # ... HostedCluster spec
```

## Additional JSON Patch Examples

The JSON patch annotation is not limited to node affinity. Below are additional
examples showing other use cases.

### Replacing CPU Cores

```yaml linenums="1"
metadata:
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "replace",
          "path": "/spec/template/spec/domain/cpu/cores",
          "value": 8
        }
      ]
```

### Adding a Secondary Multus Network

```yaml linenums="1"
metadata:
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      [
        {
          "op": "add",
          "path": "/spec/template/spec/networks/-",
          "value": {
            "name": "secondary-net",
            "multus": {
              "networkName": "my-namespace/my-nad"
            }
          }
        },
        {
          "op": "add",
          "path": "/spec/template/spec/domain/devices/interfaces/-",
          "value": {
            "name": "secondary-net",
            "bridge": {}
          }
        }
      ]
```

## Important Considerations

- **Validation**: The annotation value is validated by admission webhooks during
  create and update operations. Invalid JSON, missing required fields, or
  unsupported operations will be rejected.

- **Preserve default affinity**: HyperShift sets `PodAntiAffinity` or
  `TopologySpreadConstraints` by default to distribute VMs across nodes. Always
  add node affinity at the `/spec/template/spec/affinity/nodeAffinity` sub-path
  rather than replacing the entire affinity object, to avoid removing these
  defaults.

- **Precedence**: When the annotation is set on both a `HostedCluster` and a
  `NodePool`, the `HostedCluster` patches are applied first. NodePool patches
  can override values previously set by HostedCluster patches.

- **Path syntax**: Paths follow the [JSON Pointer (RFC 6901)](https://datatracker.ietf.org/doc/html/rfc6901)
  specification. Special characters in keys must be escaped: `~0` for `~` and
  `~1` for `/`.
