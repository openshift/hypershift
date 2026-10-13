In a Hosted Control Plane (HCP) architecture, the etcd database plays a critical role as the core data store for the
Hosted Kubernetes control plane components. By default, Hypershift provisions etcd data on a Persistent Volume Claim (PVC),
which relies on the default StorageClass defined in the Management Cluster.

However, HyperShift allows you to easily choose another storage class when desired. 
On OpenStack, the default RWO StorageClass is generally Cinder via its CSI driver to provision storage.
While this driver is suitable for general workloads, it is not ideal for etcd due to the latency and performance characteristics
of network-attached storage.
Instead, a more optimal approach is to create a StorageClass backed by high performance local disk(s) and use it explicitly for etcd.

This document provides detailed instructions on how to configure and leverage Logical Volume Manager Storage through the TopoLVM CSI
driver to dynamically provision storage for etcd.

Logical Volume Manager (LVM) Storage uses LVM2 through the TopoLVM CSI driver to dynamically provision local storage on a cluster with limited resources.
With this method, we can create volume groups, persistent volume claims (PVCs), volume snapshots, and volume clones for etcd.


1. Follow the official procedure to [Install LVM Storage by using the CLI](https://docs.openshift.com/container-platform/4.17/storage/persistent_storage/persistent_storage_local/persistent-storage-using-lvms.html#install-lvms-operator-cli_logical-volume-manager-storage) on the Management Cluster.

2. You will need worker nodes with additional ephemeral disk(s) on the Management cluster.
   The tested solution is to create an OpenStack Nova flavor that will create an additional ephemeral local disk attached to the instance.

```shell
openstack flavor create (...) --ephemeral 100 (...)
```

!!! note

    When a server will be created with that flavor, Nova will automatically create and attach a local ephemeral storage device to the VM (and format it in vfat).


3. Now we need to [Create a compute machine set](https://docs.openshift.com/container-platform/4.17/machine_management/creating_machinesets/creating-machineset-osp.html) that would use this flavor.

4. Scale the MachineSet to the desired replica. Note that if HostedClusters are deployed in High Availability, a minimum of 3 workers has to be deployed so the pods can be distributed accordingly.

5. Add a label to identify the Nodes with the ephemeral disk for etcd so we can identify them as “Hypershift ready”:

!!! note

    This label is arbitrary and can be changed.

6. Now that we have the workers that will be used for Hypershift, we can create the LVMCluster object that will describe the configuration of the local storage used for etcd in Hypershift. Create a file named “lvmcluster.yaml”:

```yaml
---
apiVersion: lvm.topolvm.io/v1alpha1
kind: LVMCluster
metadata:
  name: etcd-hcp
  namespace: openshift-storage
spec:
  storage:
    deviceClasses:
    - name: etcd-class
      default: true
      nodeSelector:
    	 nodeSelectorTerms:
    	 - matchExpressions:
      	   - key: hypershift-capable
            operator: In
            values:
            - "true"
      deviceSelector:
        forceWipeDevicesAndDestroyAllData: true
        paths:
        - /dev/vdb
```

!!! note

    * We assume that the ephemeral disk is located on /dev/vdb which is the case in most situations. However this needs to be verified when following this procedure. Using symlinks isn't supported.
    * `forceWipeDevicesAndDestroyAllData` is set to True because the default nova ephemeral disk comes formatted in vfat.
    * `thinPoolConfig` can be used but it will affect the performance therefore we don't recommend it.

Now we create the resource: 

```shell
oc apply -f lvmcluster.yaml
```

We can verify that the resource has been created:

```shell
oc get lvmcluster -A
NAMESPACE       	NAME   	STATUS
openshift-storage   etcd-hcp   Ready
```

Also we can verify that we have a StorageClass for LVM:

```shell
oc get storageclass -A
NAME                 	PROVISIONER            	  RECLAIMPOLICY   VOLUMEBINDINGMODE  	ALLOWVOLUMEEXPANSION   AGE
lvms-etcd-class      	topolvm.io             	  Delete      	  WaitForFirstConsumer  true               	   23m
standard-csi (default)  cinder.csi.openstack.org  Delete      	  WaitForFirstConsumer  true               	   56m
```


7. Now that local storage is available through CSI in the Management Cluster, the HostedCluster can be deployed this way:

```shell
hcp create cluster openstack \
	(...)
	--etcd-storage-class lvms-etcd-class
```

8. You can now verify that etcd is placed on local ephemeral storage:

```shell
oc get pvc -A
NAMESPACE              	NAME                 	STATUS   VOLUME                                 	CAPACITY   ACCESS MODES   STORAGECLASS  	VOLUMEATTRIBUTESCLASS   AGE
clusters-emacchi-hcp   	data-etcd-0          	Bound	pvc-f8b4070f-0d11-48b7-93d3-cc2a56ada7e9   8Gi    	RWO        	lvms-etcd-class   <unset>             	8s
```

In the Hosted Control Plane etcd pod:

```shell
/var/lib/data
bash-5.1$ df -h
Filesystem                                     	Size  Used Avail Use% Mounted on
(...)                                         	3.2G   86M  3.1G   3% /etc/passwd
/dev/topolvm/0b1114b3-f084-4c1c-bda5-85ef197459aa  8.0G  215M  7.8G   3% /var/lib
(...)
```

We can see the 8GB device for etcd.
