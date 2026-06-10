# Nested Virtualization on AWS EC2 NodePools — Validation Report

## Overview

This document describes the manual end-to-end validation of the nested virtualization feature for AWS EC2 NodePools in HyperShift (PR [#8681](https://github.com/openshift/hypershift/pull/8681)).

The feature adds a `cpuOptions.nestedVirtualization` field to `AWSNodePoolPlatform`, enabling users to launch EC2 instances with hardware-assisted nested virtualization. This allows workloads such as KubeVirt / OpenShift Virtualization to run VMs inside the hosted cluster worker nodes.

## NodePool Configuration

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: virt-test-us-east-2a
  namespace: clusters
spec:
  arch: amd64
  clusterName: virt-test
  replicas: 2
  release:
    image: registry.ci.openshift.org/ocp/release:4.23.0-0.nightly-2026-07-01-224445
  platform:
    type: AWS
    aws:
      cpuOptions:
        nestedVirtualization: enabled
      instanceType: m8i.2xlarge
      instanceProfile: <worker-instance-profile>
      rootVolume:
        size: 120
        type: gp3
      subnet:
        id: <private-subnet-id>
  management:
    autoRepair: false
    upgradeType: Replace
```

The key addition is `spec.platform.aws.cpuOptions.nestedVirtualization: enabled`.

## Supported Instance Types

AWS exposes nested virtualization through two different CPU option parameters depending on the processor vendor:

| Processor | Instance Families | AWS API Parameter | Boot Mode Required |
|-----------|------------------|-------------------|--------------------|
| Intel (8th gen) | `c8i`, `m8i`, `r8i` (and flex variants) | `NestedVirtualization` | `legacy-bios` or `uefi` |
| AMD | `m6a`, `c6a`, `r6a` | `AmdSevSnp` | `uefi` only |

During testing, we found that:

- **Intel `m8i.2xlarge`** works with the standard RHCOS x86_64 AMI (legacy-bios boot mode).
- **AMD `m6a.2xlarge`** requires a UEFI boot mode AMI, which the current RHCOS x86_64 AMIs do not provide.
- **Intel `m6i.2xlarge`** (6th gen) does not support nested virtualization at all.

The CAPA (Cluster API Provider AWS) controller automatically maps the `nestedVirtualization: enabled` field to the correct AWS API parameter based on the instance type.

## Validation Steps

### 1. Feature Propagation: NodePool → AWSMachineTemplate → AWSMachine

Verified that `cpuOptions.nestedVirtualization: enabled` propagates through the CAPI resource chain:

```
NodePool (spec.platform.aws.cpuOptions.nestedVirtualization: enabled)
  └─▶ AWSMachineTemplate (spec.template.spec.cpuOptions.nestedVirtualization: enabled)
       └─▶ AWSMachine (spec.cpuOptions.nestedVirtualization: enabled)
            └─▶ EC2 RunInstances (CpuOptions.NestedVirtualization: enabled)
```

### 2. EC2 API Confirmation

Queried the AWS EC2 API to confirm instances launched with the correct CPU options:

```json
{
    "InstanceId": "i-058d9ad21e056af6e",
    "InstanceType": "m8i.2xlarge",
    "CpuOptions": {
        "CoreCount": 4,
        "ThreadsPerCore": 2,
        "NestedVirtualization": "enabled"
    }
}
```

### 3. In-Node Verification

Connected to a worker node via `oc debug` and verified the VMX capability is exposed to the guest OS:

| Check | Result |
|-------|--------|
| `vmx` flag in `/proc/cpuinfo` | ✅ Present on all 16 vCPUs |
| `/dev/kvm` device | ✅ Available (`crw-rw-rw-`) |
| `kvm_intel` kernel module | ✅ Loaded |

Without nested virtualization enabled on the EC2 instance, the `vmx` flag would be hidden, `/dev/kvm` would not exist, and the `kvm_intel` module would not load.

### 4. Cluster Health

The full hosted cluster reached a healthy state with the 4.23 CI payload:

| Component | Status |
|-----------|--------|
| HostedCluster | `Available=True`, `Degraded=False` |
| ClusterVersion | `Available=True`, `Progressing=False` |
| NodePool (2/2) | `Ready=True`, `AllMachinesReady=True`, `AllNodesHealthy=True` |
| Control Plane Pods | 44/44 Running/Completed |
| Worker Nodes | 2x Ready, Kubernetes v1.35.3, RHCOS 9.8 |

## Test Environment

- **Management cluster**: ROSA 4.20.26 (us-east-2)
- **HyperShift operator image**: Custom build from `virt-on-new-aws-nodes` branch (`quay.io/jjaggars/hypershift:virt-on-new-aws-nodes`)
- **CPO image**: Same custom image (set via `control-plane-operator-image` annotation)
- **Release payload**: `4.23.0-0.nightly-2026-07-01-224445`
- **Worker instance type**: `m8i.2xlarge` (Intel 8th gen, 4 cores / 8 threads)
- **Region**: us-east-2
