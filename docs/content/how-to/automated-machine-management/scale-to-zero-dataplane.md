---
title: Scaling down data plane to Zero
---

# Scaling down data plane to Zero

## Context and Considerations

The main reason to go through this scenario it's mainly to save resources and money, once you are not using an already created Hosted Control Plane.

In order to continue with the next steps, we need to have in mind some considerations:

- **This is a destructive action**, all the workloads in the worker nodes will disappear.
- The Hosted Control Plane will stay up and running, and you can scale up the *NodePool* whenever you want.
- Some pods in the control plane will stay in "Pending" state.  
- Once you rescale the *NodePool/s* it will take time until they reach the fully **Ready** state.
- We will add an annotate to the nodes which will ensure the pod drainning does not happen. This we will save time and money and also we will avoid stuck pods.

Now let's explain the workflow.

## Workflow

### Limitations and Caveats

To temporarily remove your data plane (Compute Nodes) in the *HostedCluster* all you need to do is scaling all *NodePools* down to zero. The default draining policy will protect any workloads, trying to move the pods to other available nodes. You can get around that policy deliberately by setting drainTimeout to a lower value, but this option has a caveat:

- **Caveat**: Changing *drainTimeout* in an existing *NodePool* will trigger a rolling update first and so the new value will only affect to the new created *Machines*. If you are in a situation where the intent is skip draining and don't want to assume the rolling upgrade triggered by changing the field, you can skip draining forcefully in a given *Machine* by setting `"machine.cluster.x-k8s.io/exclude-node-draining="` annotation in the *HostedCluster* machines as we will follow in this document.

This is a known limitation and we plane to prevent changing *drainTimeout* from triggering a rolling update in the future.

**NOTE**: `machines.machine.cluster.x-k8s.io` are considered a lower level resource and any consumer interaction is recommended to happen via *NodePool*.

### Procedure

This is a not difficult procedure but you need to be sure that you have set the right `KUBECONFIG` and you point to the correct context because maybe you already have some Kubeconfigs or contexts in the same file and we wanna work over the Management cluster.

- Set the right *Kubeconfig/Context* to work over the Management Cluster (The one where you have Hypershift installed)
- Now you need to annotate the *Machine* object from the Hosted Control Plane namespace with `"machine.cluster.x-k8s.io/exclude-node-draining="`. This annotation will allow the Cluster API to avoid the Pod drainning for every *Machine* you wanna shutdown, that means, the AWS Instance deletion will be done instantly and Openshift will not try to drain the Pods and move them over other node.

<details>
<summary> How to annotate the *Machine* objects </summary>

You can do it manually using this command:
```bash
oc annotate -n <HostedClusterNamespace>-<HostedClusterName> machines --all "machine.cluster.x-k8s.io/exclude-node-draining="
```

or execute this script:

```bash
#!/bin/bash

function annotate_nodes() {
    MACHINES="$(oc get machines -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name | wc -l)"
    if [[ ${MACHINES} -le 0 ]];then
        echo "There is not machines or machineSets in the Hosted ControlPlane namespace, exiting..."
        echo "HC Namespace: ${HC_CLUSTER_NS}"
        echo "HC Clusted Name: ${HC_CLUSTER_NAME}"
        exit 1
    fi

    echo "Annotating Nodes to avoid Draining"
    oc annotate -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} machines --all "machine.cluster.x-k8s.io/exclude-node-draining="
    echo "Nodes annotated!"
}


## Fill these variables first
export KUBECONFIG=<KubeconfigPath>
export HC_CLUSTER_NS=<HostedClusterNamespace>
export HC_CLUSTER_NAME=<HostedClusterName>

CHECK_NS="$(oc get ns -o name ${2})"
if [[ -z "${CHECK_NS}" ]];then
    echo "Namespace does not exists in the Management Cluster"
    exit 1
fi

CHECK_HC="$(oc get hc -n ${HC_CLUSTER_NS} -o name ${3})"
if [[ -z "${CHECK_HC}" ]];then
    echo "HC ${3} does not exists in the namespace ${2} of the Management Cluster"
    exit 1
fi

annotate_nodes
```

</details>

- The next step it's basically scale down the *NodePool* associated to our *HostedCluster*. In order to identify it we need to execute an `oc get nodepool -n <HostedCluster Namespace>`, the one where you created the *HostedCluster* and *NodePool* on deployment time.
- To perform the scale down, you just need to grab the *NodePool* name from the last command and execute this other one `oc scale nodepool/<NodePool Name> --namespace <HostedCluster Namespace> --replicas=0`


<details>
<summary> How to scale down the nodes programmatically </summary>

```bash
#!/bin/bash

function scale_down_pool() {

    # Validated that the nodes in AWS Scale down instantly, they take sometime to disappear inside of Openshift
    # but the draining is avoided for sure
    echo "Scalling down the nodes for ${HC_CLUSTER_NAME} cluster"
    NODEPOOLS=$(oc get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')
    oc scale nodepool/${NODEPOOLS} --namespace ${HC_CLUSTER_NS} --replicas=0
    echo "NodePool ${NODEPOOLS} scaled down!"
}

## Fill these variables first
export KUBECONFIG=<KubeconfigPath>
export HC_CLUSTER_NS=<HostedClusterNamespace>
export HC_CLUSTER_NAME=<HostedClusterName>

CHECK_NS="$(oc get ns -o name ${2})"
if [[ -z "${CHECK_NS}" ]];then
    echo "Namespace does not exists in the Management Cluster"
    exit 1
fi

CHECK_HC="$(oc get hc -n ${HC_CLUSTER_NS} -o name ${3})"
if [[ -z "${CHECK_HC}" ]];then
    echo "HC ${3} does not exists in the namespace ${2} of the Management Cluster"
    exit 1
fi

scale_down_nodepool
```

</details>

After these steps, you will see how the (in the AWS case) instances will be terminated instantly, but Openshift will take some time until the nodes get deleted because of the default timeouts set on the platforms.
