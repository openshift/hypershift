This document explains how to create a HostedCluster that runs an SDN provider different from OVNKubernetes. The document assumes that you already have the required infrastructure in place to create HostedClusters.

!!! important

    The work described here is **not supported**. SDN providers **must** certify their software on HyperShift before it becomes a supported solution. The steps described here are just a technical reference for people who wants to try different SDN providers in HyperShift.

Versions used while writing this doc:

- Management cluster running OpenShift `v4.14.5` and HyperShift Operator version `e87182ca75da37c74b371aa0f17aeaa41437561a`.
- HostedCluster release set to OpenShift `v4.14.10`.

!!! important

    To configure a different CNI provider for the Hosted Cluster, you must adjust the `hostedcluster.spec.networking.networkType` to `Other`. By doing so, the Control Plane Operator will skip the deployment of the default CNI provider.

## Calico
### Deployment

In this scenario we are using the Calico version v3.27.0 which is the last one at the time of this writing. The steps followed rely on the [docs](https://docs.tigera.io/calico/latest/getting-started/kubernetes/openshift/installation#generate-the-install-manifests) by Tigera to deploy Calico on OpenShift.

1. Create a `HostedCluster` and set its `HostedCluster.spec.networking.networkType` to `Other`.

2. Wait for the HostedCluster's API to be ready. Once it's ready, get the admin kubeconfig.

3. Eventually the compute nodes will show up in the cluster. Keep in mind since the SDN is not deployed yet, they will remain in `NotReady` state.

    ~~~sh
    export KUBECONFIG=/path/to/hostedcluster/admin/kubeconfig
    oc get nodes
    ~~~

    ~~~output
    NAME             STATUS     ROLES    AGE     VERSION
    hosted-worker1   NotReady   worker   2m51s   v1.27.8+4fab27b
    hosted-worker2   NotReady   worker   2m52s   v1.27.8+4fab27b
    ~~~

4. Apply the yaml manifests provided by `Tigera` in the HostedCluster:

    ~~~sh
    mkdir calico
    wget -qO- https://github.com/projectcalico/calico/releases/download/v3.27.0/ocp.tgz | tar xvz --strip-components=1 -C calico
    cd calico/
    ls *crd*.yaml | xargs -n1 oc apply -f
    ls 00* | xargs -n1 oc apply -f
    ls 01* | xargs -n1 oc apply -f
    ls 02* | xargs -n1 oc apply -f
    ~~~

    ~~~output
    customresourcedefinition.apiextensions.k8s.io/bgpconfigurations.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/bgpfilters.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/bgppeers.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/blockaffinities.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/caliconodestatuses.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/clusterinformations.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/felixconfigurations.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/globalnetworkpolicies.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/globalnetworksets.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/hostendpoints.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/ipamblocks.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/ipamconfigs.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/ipamhandles.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/ippools.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/ipreservations.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/kubecontrollersconfigurations.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/networkpolicies.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/networksets.crd.projectcalico.org created
    customresourcedefinition.apiextensions.k8s.io/apiservers.operator.tigera.io created
    customresourcedefinition.apiextensions.k8s.io/imagesets.operator.tigera.io created
    customresourcedefinition.apiextensions.k8s.io/installations.operator.tigera.io created
    customresourcedefinition.apiextensions.k8s.io/tigerastatuses.operator.tigera.io created
    namespace/calico-apiserver created
    namespace/calico-system created
    namespace/tigera-operator created
    apiserver.operator.tigera.io/default created
    installation.operator.tigera.io/default created
    configmap/calico-resources created
    clusterrolebinding.rbac.authorization.k8s.io/tigera-operator created
    clusterrole.rbac.authorization.k8s.io/tigera-operator created
    serviceaccount/tigera-operator created
    deployment.apps/tigera-operator created
    ~~~

### Checks

We should see the following pods running in the `tigera-operator` namespace:

  ~~~sh
  oc -n tigera-operator get pods
  ~~~

  ~~~output
  NAME                              READY   STATUS    RESTARTS   AGE
  tigera-operator-dc7c9647f-fvcvd   1/1     Running   0          2m1s
  ~~~

We should see the following pods running in the `calico-system` namespace:

  ~~~sh
  oc -n calico-system get pods
  ~~~

  ~~~output
  NAME                                       READY   STATUS    RESTARTS   AGE
  calico-kube-controllers-69d6d5ff89-5ftcn   1/1     Running   0          2m1s
  calico-node-6bzth                          1/1     Running   0          2m2s
  calico-node-bl4b6                          1/1     Running   0          2m2s
  calico-typha-6558c4c89d-mq2hw              1/1     Running   0          2m2s
  csi-node-driver-l948w                      2/2     Running   0          2m1s
  csi-node-driver-r6rgw                      2/2     Running   0          2m2s
  ~~~

We should see the following pods running in the `calico-apiserver` namespace:

  ~~~sh
  oc -n calico-apiserver get pods
  ~~~

  ~~~output
  NAME                                READY   STATUS    RESTARTS   AGE
  calico-apiserver-7bfbf8fd7c-d75fw   1/1     Running   0          84s
  calico-apiserver-7bfbf8fd7c-fqxjm   1/1     Running   0          84s
  ~~~

The nodes should've moved to `Ready` state:

  ~~~sh
  oc get nodes
  ~~~

  ~~~output
  NAME             STATUS   ROLES    AGE   VERSION
  hosted-worker1   Ready    worker   10m   v1.27.8+4fab27b
  hosted-worker2   Ready    worker   10m   v1.27.8+4fab27b
  ~~~

The HostedCluster deployment will continue, at this point the SDN is running.

## Cilium
### Deployment

In this scenario, we are using Cilium version v1.14.5, which is the latest release at the time of writing. The steps followed rely on the [docs](https://docs.cilium.io/en/stable/installation/k8s-install-openshift-okd/) by Cilium project to deploy Cilium on OpenShift.

1. Create a `HostedCluster` and set its `HostedCluster.spec.networking.networkType` to `Other`.

2. Wait for the HostedCluster's API to be ready. Once it's ready, get the admin kubeconfig.

3. Eventually the compute nodes will show up in the cluster. Keep in mind since the SDN is not deployed yet, they will remain in `NotReady` state.

    ~~~sh
    export KUBECONFIG=/path/to/hostedcluster/admin/kubeconfig
    oc get nodes
    ~~~

    ~~~output
    NAME             STATUS     ROLES    AGE     VERSION
    hosted-worker1   NotReady   worker   2m30s   v1.27.8+4fab27b
    hosted-worker2   NotReady   worker   2m33s   v1.27.8+4fab27b
    ~~~

4. Apply the yaml manifests provided by `Isovalent` in the HostedCluster:

    ~~~sh
    #!/bin/bash

    version="1.14.5"
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-03-cilium-ciliumconfigs-crd.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00000-cilium-namespace.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00001-cilium-olm-serviceaccount.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00002-cilium-olm-deployment.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00003-cilium-olm-service.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00004-cilium-olm-leader-election-role.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00005-cilium-olm-role.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00006-leader-election-rolebinding.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00007-cilium-olm-rolebinding.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00008-cilium-cilium-olm-clusterrole.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00009-cilium-cilium-clusterrole.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00010-cilium-cilium-olm-clusterrolebinding.yaml
    oc apply -f https://raw.githubusercontent.com/isovalent/olm-for-cilium/main/manifests/cilium.v${version}/cluster-network-06-cilium-00011-cilium-cilium-clusterrolebinding.yaml
    ~~~


5. Use the right configuration for each network stack

=== "IPv4"

    ~~~yaml
    apiVersion: cilium.io/v1alpha1
    kind: CiliumConfig
    metadata:
      name: cilium
      namespace: cilium
    spec:
      debug:
        enabled: true
      k8s:
        requireIPv4PodCIDR: true
      logSystemLoad: true
      bpf:
        preallocateMaps: true
      etcd:
        leaseTTL: 30s
      ipv4:
        enabled: true
      ipv6:
        enabled: false
      identityChangeGracePeriod: 0s
      ipam:
        mode: "cluster-pool"
        operator:
          clusterPoolIPv4PodCIDRList:
            - "10.128.0.0/14"
          clusterPoolIPv4MaskSize: "23"
      nativeRoutingCIDR: "10.128.0.0/14"
      endpointRoutes: {enabled: true}
      clusterHealthPort: 9940
      tunnelPort: 4789
      cni:
        binPath: "/var/lib/cni/bin"
        confPath: "/var/run/multus/cni/net.d"
        chainingMode: portmap
      prometheus:
        serviceMonitor: {enabled: false}
      hubble:
        tls: {enabled: false}
      sessionAffinity: true
    ~~~

    ~~~sh
    oc apply -f ciliumconfig.yaml
    ~~~

=== "IPv6"

    ~~~yaml
    apiVersion: cilium.io/v1alpha1
    kind: CiliumConfig
    metadata:
      name: cilium
      namespace: cilium
    spec:
      debug:
        enabled: true
      k8s:
        requireIPv6PodCIDR: true
      logSystemLoad: true
      bpf:
        preallocateMaps: true
      etcd:
        leaseTTL: 30s
      ipv4:
        enabled: false
      ipv6:
        enabled: true
      identityChangeGracePeriod: 0s
      ipam:
        mode: "cluster-pool"
        operator:
          clusterPoolIPv6PodCIDRList:
            - "fd01::/48"
          clusterPoolIPv6MaskSize: "48"
      nativeRoutingCIDR: "fd01::/48"
      endpointRoutes: {enabled: true}
      clusterHealthPort: 9940
      tunnelPort: 4789
      cni:
        binPath: "/var/lib/cni/bin"
        confPath: "/var/run/multus/cni/net.d"
        chainingMode: portmap
      prometheus:
        serviceMonitor: {enabled: false}
      hubble:
        tls: {enabled: false}
      sessionAffinity: true
    ~~~

    ~~~sh
    oc apply -f ciliumconfig.yaml
    ~~~


=== "Dual stack"

    ~~~yaml
    apiVersion: cilium.io/v1alpha1
    kind: CiliumConfig
    metadata:
      name: cilium
      namespace: cilium
    spec:
      debug:
        enabled: true
      k8s:
        requireIPv4PodCIDR: true
      logSystemLoad: true
      bpf:
        preallocateMaps: true
      etcd:
        leaseTTL: 30s
      ipv4:
        enabled: true
      ipv6:
        enabled: true
      identityChangeGracePeriod: 0s
      ipam:
        mode: "cluster-pool"
        operator:
          clusterPoolIPv4PodCIDRList:
            - "10.128.0.0/14"
          clusterPoolIPv4MaskSize: "23"
      nativeRoutingCIDR: "10.128.0.0/14"
      endpointRoutes: {enabled: true}
      clusterHealthPort: 9940
      tunnelPort: 4789
      cni:
        binPath: "/var/lib/cni/bin"
        confPath: "/var/run/multus/cni/net.d"
        chainingMode: portmap
      prometheus:
        serviceMonitor: {enabled: false}
      hubble:
        tls: {enabled: false}
      sessionAffinity: true
    ~~~

    ~~~sh
    oc apply -f ciliumconfig.yaml
    ~~~

!!! important

    Make sure you've changed the networking values according to your platform details `spec.ipam.operator.clusterPoolIPv4PodCIDRList`, `spec.ipam.operator.clusterPoolIPv4MaskSize` and `nativeRoutingCIDR` in IPv4 and `spec.ipam.operator.clusterPoolIPv6PodCIDRList`, `spec.ipam.operator.clusterPoolIPv6MaskSize` and `nativeRoutingCIDR` in IPv6 case.

### Checks

This will be the output:

  ~~~output
  customresourcedefinition.apiextensions.k8s.io/ciliumconfigs.cilium.io created
  namespace/cilium created
  serviceaccount/cilium-olm created
  Warning: would violate PodSecurity "restricted:v1.24": host namespaces (hostNetwork=true), hostPort (container "operator" uses hostPort 9443), allowPrivilegeEscalation != false (container "operator" must set securityContext.allowPrivilegeEscalation=false), unrestricted capabilities (container "operator" must set securityContext.capabilities.drop=["ALL"]), runAsNonRoot != true (pod or container "operator" must set securityContext.runAsNonRoot=true), seccompProfile (pod or container "operator" must set securityContext.seccompProfile.type to "RuntimeDefault" or "Localhost")
  deployment.apps/cilium-olm created
  service/cilium-olm created
  role.rbac.authorization.k8s.io/cilium-olm-leader-election created
  role.rbac.authorization.k8s.io/cilium-olm created
  rolebinding.rbac.authorization.k8s.io/leader-election created
  rolebinding.rbac.authorization.k8s.io/cilium-olm created
  clusterrole.rbac.authorization.k8s.io/cilium-cilium-olm created
  clusterrole.rbac.authorization.k8s.io/cilium-cilium created
  clusterrolebinding.rbac.authorization.k8s.io/cilium-cilium-olm created
  clusterrolebinding.rbac.authorization.k8s.io/cilium-cilium created
  ciliumconfig.cilium.io/cilium created
  ~~~

We should see the following pods running in the `cilium` namespace:

  ~~~sh
  oc -n cilium get pods
  ~~~

  ~~~output
  NAME                               READY   STATUS    RESTARTS   AGE
  cilium-ds5tr                       1/1     Running   0          106s
  cilium-olm-7c9cf7c948-txkvt        1/1     Running   0          2m36s
  cilium-operator-595594bf7d-gbnns   1/1     Running   0          106s
  cilium-operator-595594bf7d-mn5wc   1/1     Running   0          106s
  cilium-wzhdk                       1/1     Running   0          106s
  ~~~

The nodes should've moved to `Ready` state:

  ~~~sh
  oc get nodes
  ~~~

  ~~~output
  NAME             STATUS   ROLES    AGE   VERSION
  hosted-worker1   Ready    worker   8m    v1.27.8+4fab27b
  hosted-worker2   Ready    worker   8m    v1.27.8+4fab27b
  ~~~

The HostedCluster deployment will continue, at this point the SDN is running.

### Validation

Additionally you have some conformance tests that could be deployed into the HostedCluster in order to validate if the Cilium SDN was deployed and working properly.

In order for Cilium connectivity test pods to run on OpenShift, a simple custom SecurityContextConstraints object is required to allow hostPort/hostNetwork which the connectivity test pods relies on. it should only set allowHostPorts and allowHostNetwork without any other privileges.

  ~~~sh
  oc apply -f - <<EOF
  apiVersion: security.openshift.io/v1
  kind: SecurityContextConstraints
  metadata:
    name: cilium-test
  allowHostPorts: true
  allowHostNetwork: true
  users:
    - system:serviceaccount:cilium-test:default
  priority: null
  readOnlyRootFilesystem: false
  runAsUser:
    type: MustRunAsRange
  seLinuxContext:
    type: MustRunAs
  volumes: null
  allowHostDirVolumePlugin: false
  allowHostIPC: false
  allowHostPID: false
  allowPrivilegeEscalation: false
  allowPrivilegedContainer: false
  allowedCapabilities: null
  defaultAddCapabilities: null
  requiredDropCapabilities: null
  groups: null
  EOF
  ~~~

  ~~~sh
  version="1.14.5"
  oc apply -n cilium-test -f https://raw.githubusercontent.com/cilium/cilium/${version}/examples/kubernetes/connectivity-check/connectivity-check.yaml
  ~~~

  ~~~sh
  oc get pod -n cilium-test
  ~~~

  ~~~output
  NAME                                                     READY   STATUS    RESTARTS   AGE
  echo-a-846dcb4-kq7zh                                     1/1     Running   0          23h
  echo-b-58f67d5b86-5mrtx                                  1/1     Running   0          23h
  echo-b-host-84d7468c8d-nf4vk                             1/1     Running   0          23h
  host-to-b-multi-node-clusterip-b98ff785c-b9vgf           1/1     Running   0          23h
  host-to-b-multi-node-headless-5c55d85dfc-5xjbc           1/1     Running   0          23h
  pod-to-a-6b996b7675-46kkf                                1/1     Running   0          23h
  pod-to-a-allowed-cnp-c958b55bf-6vskb                     1/1     Running   0          23h
  pod-to-a-denied-cnp-6d9b8cbff5-lbrgp                     1/1     Running   0          23h
  pod-to-b-intra-node-nodeport-5f9c4c866f-mhfs4            1/1     Running   0          23h
  pod-to-b-multi-node-clusterip-7cb4bf5495-hmmtg           1/1     Running   0          23h
  pod-to-b-multi-node-headless-68975fc557-sqbgq            1/1     Running   0          23h
  pod-to-b-multi-node-nodeport-559c54c6fc-2rhvv            1/1     Running   0          23h
  pod-to-external-1111-5c4cfd9497-6slss                    1/1     Running   0          23h
  pod-to-external-fqdn-allow-google-cnp-7d65d9b747-w4cx5   1/1     Running   0          23h
  ~~~
