# Configuring Machines in HyperShift

In standalone OpenShift, machine configuration is managed via resources like MachineConfig, KubeletConfig, and ContainerRuntimeConfig inside the cluster, and then applied to a set of machines via a MachineConfigPool. In HyperShift clusters, machine configuration is not managed from within a hosted cluster, but rather via a NodePool resource in the hosting cluster.

The NodePool field `.spec.config` can be populated with a list of references to configmaps that contain MachineConfig, KubeletConfig, or ContainerRuntimeConfig manifests. This configuration is applied to all machines belonging to that NodePool.

## Creating a NodePool with custom configuration

### 1. Create MachineConfig and ContainerRuntimeConfig manifests

```shell
cat <<EOF > machine-config.yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: example-machineconfig
spec:
  config:
    storage:
      files:
        - path: /etc/custom.cfg
          mode: 420
          contents:
            source: data:text/plain;charset=utf-8;base64,ZXhhbXBsZSBjb25maWd1cmF0aW9u
  systemd:
    units:
      - name: example-service.service
        enabled: true
        contents: |
          [Unit]
          Description=Example Service
          
          [Service]
          ExecStart=/usr/local/bin/example-service
          Restart=always
EOF

cat <<EOF > cr-config.yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: example-container-runtime-config
spec:
  containerRuntimeConfig:
    pidsLimit: 30
EOF
```

### 2. Create ConfigMaps for every configuration resource

```shell
oc create configmap example-machineconfig -n clusters --from-file config=machine-config.yaml 
oc create configmap example-crc -n clusters --from-file config=cr-config.yaml
```

NOTE: the key in each ConfigMap must be `config`

### 3. Create a NodePool that references these configurations

```shell
cat <<EOF > custom-nodepool.yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: custom-nodepool
  namespace: clusters
spec:
  arch: amd64
  clusterName: example-cluster
  config:
  - name: example-machineconfig
  - name: example-crc
  management:
    autoRepair: true
    replace:
      rollingUpdate:
        maxSurge: 1
        maxUnavailable: 0
      strategy: RollingUpdate
    upgradeType: Replace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    aws:
      instanceProfile: example-profile
      instanceType: m5.xlarge
      rootVolume:
        size: 120
        type: gp3
    type: AWS
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.14.6-x86_64
  replicas: 2
```

```shell
oc apply -f custom-nodepool.yaml
```