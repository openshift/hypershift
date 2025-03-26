# Additional ports for Nodepools

This document describes how to attach additional ports to nodes in a HostedCluster on OpenStack.

## Use-cases

- **SR-IOV**: Single Root I/O Virtualization (SR-IOV) is a specification that allows a single Peripheral Component Interconnect Express (PCIe) physical device to appear as multiple separate physical devices. This can be useful for high-performance networking scenarios. The Nodepool can be configured to use SR-IOV by attaching additional ports to the nodes. Workloads that require high-performance networking can use the SR-IOV devices.
- **DPDK**: Data Plane Development Kit (DPDK) is a set of libraries and drivers for fast packet processing. DPDK can be used to improve the performance of network applications. The Nodepool can be configured to use DPDK by attaching additional ports to the nodes. Workloads that require high-performance networking can use the DPDK devices.
- **Manila RWX on NFS**: Manila ReadWriteMany (RWX) volumes can be used by multiple nodes in a HostedCluster. The Nodepool can be configured to use Manila RWX volumes by attaching additional ports to the nodes. Workloads that require shared storage will be able to reach the NFS network configured for the Manila RWX volumes.
- **Multus CNI**: Multus is a meta-plugin for Kubernetes CNI that enables attaching multiple network interfaces to pods. The Nodepool can be configured to use Multus by attaching additional ports to the nodes. Workloads that require multiple network interfaces will be able to use the Multus CNI. For example it's useful if the workload needs to connect to IPv6 networks (dual-stack or single-stack).

## Prerequisites

Additional networks must be created in OpenStack and the project used by the HostedCluster must have access to these networks.

## Available options

The `--openstack-node-additional-port` flag can be used to attach additional ports to nodes in a HostedCluster on OpenStack. The flag takes a list of parameters separated by commas. The parameter can be used multiple times to attach multiple additional ports to the nodes.

The parameters are:

| Parameter       | Description                                                                                          | Required | Default  |
|-----------------|------------------------------------------------------------------------------------------------------|----------|----------|
| `network-id`    | The ID of the network to attach to the node.                                                         | Yes      | N/A      |
| `vnic-type`     | The VNIC type to use for the port. When not specified, Neutron uses the default `normal`.            | No       | N/A      |
| `disable-port-security` | Whether to enable port security for the port. When not specified, Neutron will enable it unless explicitly disabled in the network      | No       | N/A      |
| `address-pairs` | A list of IP address pairs to be assigned to the port. The format is `ip_address=mac_address`. Multiple pairs can be provided, separated by a "-". The `mac_address` portion is optional and in most cases won't be used. | No       | N/A      |

## Example

The following example demonstrates how to create a HostedCluster with additional ports attached to the nodes.

```shell
export NODEPOOL_NAME=$CLUSTER_NAME-ports
export WORKER_COUNT="2"
export FLAVOR="m1.xlarge"

hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --openstack-node-flavor $FLAVOR \
  --openstack-node-additional-port "network-id=<SRIOV_NET_ID>,vnic-type=direct,disable-port-security=true" \
  --openstack-node-additional-port "network-id=<LB_NET_ID>,address-pairs:192.168.0.1-192.168.0.2"
```

In this example, two additional ports are attached to the nodes in the Nodepool in addition to the network created by the Cluster API provider using for the control-plane. The first additional port is attached to the network with the ID `<SRIOV_NET_ID>`. The port uses the `direct` VNIC type and has port security disabled. The second port is attached to the network with the ID `<LB_NET_ID>`. The port will allow address pairs so services like MetalLB will be able to handle
the traffic on these IPs.
