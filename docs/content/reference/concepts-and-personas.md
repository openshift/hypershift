# Concepts and Personas

## Concepts

**Management Cluster**

An OpenShift cluster which hosts HyperShift and zero to many Hosted Clusters.

**Hosted Cluster**

An OpenShift API endpoint managed by HyperShift.

**Hosted Control Plane**

An OpenShift control plane running on the Management Cluster which is exposed by a Hosted Cluster's API endpoint. The component parts of a control plane include etcd, apiserver, kube-controller-manager, vpn, etc.

**Hosted Control Plane Infrastructure**

Resources on the Management Cluster or external cloud provider which are prerequisites to running Hosted Control Plane processes.

**Management Cluster Infrastructure**

Network, compute, storage, etc. of the Management Cluster.

**Hosted Cluster Infrastructure**

Network, compute, storage, etc. that exist in customer cloud account.

## Personas

**Cluster Service Provider**

The user hosting cluster control planes, responsible for up-time. UI for fleet wide alerts, configuring AWS account to host control planes in, user provisioned infra (host awareness of available compute), where to pull VMs from. Has cluster admin management.

**Cluster Service Consumer**

The user empowered to request control planes, request workers, and drive upgrades or modify externalized configuration. Likely not empowered to manage or access cloud credentials or infrastructure encryption keys.

**Cluster Instance Admin**

The user with cluster-admin role in the provisioned cluster, but may have no power over when/how cluster is upgraded or configured. May see some configuration projected into the cluster in read-only fashion.

**Cluster (Instance) User**

Maps to a developer today in standalone OCP. They will not have a view or insight into OperatorHub, Machines, etc.