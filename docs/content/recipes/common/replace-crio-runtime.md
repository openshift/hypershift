# Replacing the Worker Node's CRI-O Runtime

Starting with OpenShift version 4.18, the Machine Config Operator (MCO) will switch the default runtime from `runc` to `crun`, simplying much of the internals, crun is a simplified implementation of runc written in C. You can find more information about this change [here](https://github.com/containers/crun?tab=readme-ov-file#documentation).

## Switching from `runc` to `crun`

The most common use case is when someone using OpenShift 4.16 or 4.17 wants to test their workload performance with `crun` before upgrading to 4.18, where `crun` will become the default runtime.

Ensure you follow these steps to make the change.

### Creating the MachineConfig patch for the NodePool

The following manifest sets `crun` as the default runtime.

=== "**HCP Regular Workloads (CRUN)**"

    - Put the crio configuration into a file

    ```bash
    cat <<EOF > crio-config.conf
    [crio.runtime]
    default_runtime="crun"

    [crio.runtime.runtimes.crun]
    runtime_path = "/bin/crun"
    runtime_type = "oci"
    runtime_root = "/run/crun"
    EOF
    ```

    - Get the base64 hash of the file content

    ```bash
    export CONFIG_HASH=$(cat crio-config.conf | base64 -w0)
    ```

    - Create the MachineConfig file with content in Base64

    ```bash
    cat <<EOF > mc-crun-default-runtime.yaml
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      name: 60-crun-default-runtime
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:text/plain;charset=utf-8;base64,${CONFIG_HASH}
            mode: 420
            path: /etc/crio/crio.conf.d/99-crun-runtime.conf
    EOF
    ```

    - Run this command to create a ConfigMap containing the MachineConfig patch for regular workloads

    ```bash
    oc create -n <hostedcluster_namespace> configmap mcp-crun-default --from-file config=mc-crun-default-runtime.yaml
    ```

    - Use this command to patch the NodePool resource by adding the newly created ConfigMap as a MachineConfig change

    ```bash
    oc patch -n <hostedcluster_namespace> nodepool <nodepool_name> --type=add -p='{"spec": {"config": [{"name": "mcp-crun-default"}]}}'
    ```

=== "**HCP High Performance Workloads (CRUN)**"

    - Put the crio configuration into a file

    ```bash
    cat <<EOF > crio-config.conf
    [crio.runtime]
    default_runtime="crun"
    # You will need to adjust the CPU set assignation depending on the infrastructure
    infra_ctr_cpuset = "0-1,24-25"

    [crio.runtime.runtimes.crun]
    runtime_path = "/bin/crun"
    runtime_type = "oci"
    runtime_root = "/run/crun"

    allowed_annotations = [
        "io.containers.trace-syscall",
        "io.kubernetes.cri-o.Devices",
        "io.kubernetes.cri-o.LinkLogs",
        "cpu-load-balancing.crio.io",
        "cpu-quota.crio.io",
        "irq-load-balancing.crio.io",
    ]
    EOF
    ```

    - Get the base64 hash of the file content

    ```bash
    export CONFIG_HASH=$(cat crio-config.conf | base64 -w0)
    ```

    - Create the MachineConfig file with content in Base64

    ```bash
    cat <<EOF > mc-crun-high-perf-default-runtime.yaml
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      name: 60-crun-high-perf-default-runtime
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:text/plain;charset=utf-8;base64,${CONFIG_HASH}
            mode: 420
            path: /etc/crio/crio.conf.d/99-crun-high-perf-runtime.conf
    EOF
    ```

    - Create the ConfigMap for high-performance workloads using the following command

    ```bash
    oc create -n <hostedcluster_namespace> configmap mcp-crun-hp-default --from-file config=mc-crun-high-perf-default-runtime.yaml
    ```

    - Patch the NodePool resource with the newly created ConfigMap

    ```bash
    oc patch -n <hostedcluster_namespace> nodepool <nodepool_name> --type=add -p='{"spec": {"config": [{"name": "mcp-crun-hp-default"}]}}'
    ```

## Switching from `crun` to `runc`

If you experience performance issues or similar problems and want to revert to `runc` as the default runtime, follow this guide to set `runc` as the default CRI-O runtime for your worker nodes, even after upgrading to 4.18. This process is also compatible with 4.17 if you plan to upgrade but want to retain `runc` as the runtime.

### Creating the MachineConfig patch for the NodePool

The following manifest sets `runc` as the default runtime.

=== "**HCP Regular Workloads (RUNC)**"

    - Put the crio configuration into a file

    ```bash
    cat <<EOF > crio-config.conf
    [crio.runtime]
    default_runtime="runc"

    [crio.runtime.runtimes.runc]
    runtime_path = "/bin/runc"
    runtime_type = "oci"
    runtime_root = "/run/runc"
    EOF
    ```

    - Get the base64 hash of the file content

    ```bash
    export CONFIG_HASH=$(cat crio-config.conf | base64 -w0)
    ```

    - Create the MachineConfig file with content in Base64

    ```bash
    cat <<EOF > mc-runc-default-runtime.yaml
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      name: 60-runc-default-runtime
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:text/plain;charset=utf-8;base64,${CONFIG_HASH}
            mode: 420
            path: /etc/crio/crio.conf.d/99-runc-runtime.conf
    EOF
    ```

    - Create the ConfigMap for regular workloads using

    ```bash
    oc create -n <hostedcluster_namespace> configmap mcp-runc-default --from-file config=mc-runc-default-runtime.yaml
    ```

    - Patch the NodePool resource with the newly created ConfigMap

    ```bash
    oc patch -n <hostedcluster_namespace> nodepool <nodepool_name> --type=add -p='{"spec": {"config": [{"name": "mcp-runc-default"}]}}'
    ```

=== "**HCP High Performance Workloads (RUNC)**"

    - Put the crio configuration into a file

    ```bash
    cat <<EOF > crio-config.conf
    [crio.runtime]
    default_runtime="runc"
    # You will need to adjust the CPU set assignation depending on the infrastructure
    infra_ctr_cpuset = "0-1,24-25"

    [crio.runtime.runtimes.runc]
    runtime_path = "/bin/runc"
    runtime_type = "oci"
    runtime_root = "/run/runc"

    allowed_annotations = [
        "io.containers.trace-syscall",
        "io.kubernetes.cri-o.Devices",
        "io.kubernetes.cri-o.LinkLogs",
        "cpu-load-balancing.crio.io",
        "cpu-quota.crio.io",
        "irq-load-balancing.crio.io",
    ]
    EOF
    ```

    - Get the base64 hash of the file content

    ```bash
    export CONFIG_HASH=$(cat crio-config.conf | base64 -w0)
    ```

    ```bash
    cat <<EOF > mc-runc-high-perf-default-runtime.yaml
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      name: 60-runc-high-perf-default-runtime
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:text/plain;charset=utf-8;base64,${CONFIG_HASH}
            mode: 420
            path: /etc/crio/crio.conf.d/99-runc-high-perf-runtime.conf
    EOF
    ```

    - Create the ConfigMap for high-performance workloads

    ```bash
    oc create -n <hostedcluster_namespace> configmap mcp-runc-hp-default --from-file config=mc-runc-high-perf-default-runtime.yaml
    ```

    - Patch the NodePool resource with the newly created ConfigMap

    ```bash
    oc patch -n <hostedcluster_namespace> nodepool <nodepool_name> --type=add -p='{"spec": {"config": [{"name": "mcp-runc-hp-default"}]}}'
    ```
