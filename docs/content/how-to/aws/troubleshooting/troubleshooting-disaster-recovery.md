# Debug Disaster Recovery - Hosted Cluster Migration
These are issues related to disaster recovery that we've identified, and you could face during a Hosted Cluster migration.

## New workloads do not get scheduled in the new migrated cluster
Everything looks normal, in the destination Management or Hosted Cluster and in the old Management and Hosted Cluster, but your new workloads do not schedule in your migrated Hosted Cluster (your old ones should work properly).

Eventually your pods begin to fall down and the cluster status becomes degraded.

1. First thing you need to check is the cluster operators and validate all of them work properly:
```
oc get co
```

2. If there are some of them degraded and with errors, please check the logs and validate things point to an OVN issue.
```
oc get co <Operator's Name> -o yaml
```

3. To solve the issue, we need to ensure the old Hosted Cluster is in pause ([we also need this](https://github.com/openshift/hypershift/pull/2265)) or deleted.

4. Now we need to delete the OVN pods
```
oc --kubeconfig=${HC_KUBECONFIG} delete pod -n openshift-ovn-kubernetes --all
```

Eventually the Hosted Cluster will start self healing and the ClusterOperator will come back.

**Cause:** The cause of this issue is after the Hosted Cluster Migration the KAS (Kube API Server) uses the same DNS name, but it points to different load balancer in AWS platform. Sometimes OVN does not behave correctly facing this situation.

## The migration gets blocked in ETCD recovery

The context around it's basically "I've edited the Hosted Cluster adding the `ETCDSnapshotURL` but the modification disappears and does not continue".

The first symptom is the status of the Hypershift Operator pod, in this case the pod is usually in `CrashLoopBackOff` status.

To solve this issue we need to:

1. Kill the Hypershift Operator pod
```
oc delete pod -n hypershift -lapp=operator
```

2. Continue editing the HostedCluster, in order to add the `ETCDSnapshotURL` to the Hosted Cluster spec.

3. Now the ETCD pod will raise up using the snapshot from S3 bucket.

**Cause:** This issue happens when the Hypershift operator is down and the Hosted Cluster controller cannot handle the modifications in the objects which belong to it.

## The nodes cannot join the new Hosted Cluster and stay in the older one

We have 2 paths to follow, and it depends on if [this code](https://github.com/openshift/hypershift/pull/2265) is in your Hypershift Operator.

### The PR is merged and my Hypershift Operator has that code running

If that's the case, you need to make sure your Hosted Cluster is paused:
```
oc get hostedcluster -n <HC Namespace> <HC Name> -ojsonpath={.Spec.pausedUntil}
```

If this command does not give you any output, make sure you've followed properly the "[Disaster Recovery](https://hypershift-docs.netlify.app/how-to/aws/disaster-recovery/)" procedure, more concretelly pausing the Hosted Cluster and NodePool.

Even if it's paused and is still in that situation, please **continue to the next section** because it's highly probable that you don't have the code which manages this situation properly.

### The PR is not merged or my Hypershift Operator does not have that code running

If that's not the case, the only way to solve it is executing the teardown of the old Hosted Cluster prior the full restoration in the new Management cluster. Make sure you already have all the Manifests and the ETCD backed up.

Once you followed the Teardown procedure of the old Hosted Cluster, you will see how the migrated Hosted Cluster begins to self-recover.

**Cause:** This issue occurs when the old Hosted Cluster has a conflict with the AWSPrivateLink object. The old one is still running and the new one cannot handle it because the `hypershift.local` AWS internal DNS entry still points to the old LoadBalancer.

## Dependent resources block the old Hosted Cluster teardown

To solve this issue you need to check all the objects in the HostedControlPlane Namespace and make sure all of them are being terminated. To do that we recommend to use an external tool called [ketall](https://github.com/corneliusweig/ketall) which gives you a complete overview of all resources in a kubernetes cluster.

You need to know what object is preventing the Hosted Cluster from being deleted and ensure that the finalizer finishes successfully.

If you don't care about the stability of the old Management cluster, this script could help you to delete all the components in the **HostedControlPlane Namespace** (you need the ketall tool):

```
#!/bin/bash

####
# Execution sample:
# ./delete_ns.sh $NAMESPACE
####

NAMESPACE=$1

if [[ -z $1 ]];then
        echo "Specify the Namespace!"
        exit 1
fi

for object in $(ketall -n $NAMESPACE -o name | grep -v packa)
do
    oc -n $NAMESPACE patch $object -p '{"metadata":{"finalizers":null}}' --type merge
done
```

Eventually, the namespace will be successfully terminated and also the Hosted Cluster.

**Cause:** This is pretty common issue in the Kubernetes/Openshift world. You are trying to delete a resource that has other dependedent objects. The finalizer is still trying to delete them but it cannot progress.

## The Storage ClusterOperator keeps reporting "Waiting for Deployment"

To solve this issue you need to check that all the pods from the **HostedCluster** and the **HostedControlPlane** are running, not blocked and there are no issues in the `cluster-storage-operator` pod. After that you need to delete the **AWS EBS CSI Drivers** from the HCP namespace in the destination management cluster:

- Delete the AWS EBS CSI Drivers deployments
```
oc delete aws-ebs-csi-driver-controller aws-ebs-csi-driver-operator
```

The operator will take a while to raise up again and eventually the driver controller will be deployed by the `aws-ebs-csi-driver-operator`.

**Cause:** This issue probably comes from objects that are deployed by the Operator. In this case, `cluster-storage-operator`, but the controller or the operator does not reconcile over them. If you delete the deployments, you ensure the operator is recreated from scratch.


## The image-registry ClusterOperator keeps reporting a degraded status

When a migration is done and the image-registry clusteroperator is marked as degraded, you will need to figure out how it reaches that status. The message will look like `ImagePrunerDegraded: Job has reached the specified backoff limit`.

Things we need to review:

- Look for failure pods in the HostedControlPlane namespace at the destination management cluster.
- Check the other Cluster operators in the HostedCluster.
- Check if the nodes are ready and working fine in the HostedCluster.

If all three components are working fine, the issue is in the backoff times of the executed job `image-pruner-XXXX`; this job has most likely failed. Once the migrated cluster has already converged and looks fine, you will need to make sure to fix this cluster operator manually; you will need to determine if you want immediate resolution, or you can wait 24h and the cronjob will raise up another job by itself.

To solve it manually, you need to:

- Reexecute the job `image-pruner-xxxx` from the `openshift-image-registry` namespace, using a cronjob called `image-pruner`
```
oc create job -n openshift-image-registry --from=cronjob/image-pruner image-pruner-recover
```

This command creates a new job in that namespace and eventually will report the new status to the cluster operator.