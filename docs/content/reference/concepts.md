# Concepts

**Management Cluster**. An OpenShift cluster which hosts HyperShift and zero to many Hosted Clusters.

**Hosted Cluster**. An OpenShift API endpoint managed by HyperShift.

**Hosted Control Plane**. An OpenShift control plane running on the Management Cluster which is exposed by a Hosted Cluster's API endpoint. The component parts of a control plane include etcd, apiserver, kube-controller-manager, vpn, etc.

**Hosted Control Plane Infrastructure**. Resources on the Management Cluster or external cloud provider which are prerequisites to running Hosted Control Plane processes.

**Management Cluster Infrastructure**: network, compute, storage, etc. of the Management Cluster.

**Hosted Cluster Infrastructure**: network, compute, storage, etc. that exist in customer cloud account.
