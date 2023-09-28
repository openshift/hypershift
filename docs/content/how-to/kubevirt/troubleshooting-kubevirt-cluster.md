# Troubleshooting a KubeVirt cluster

Troubleshooting why a Hosted Control Plane with the KubeVirt platform is not
coming fully online should involve starting at the top level HostedCluster
and NodePool resources, and then working down through the stack to gain
a more detailed understanding of what has occurred until a root cause is
found.

Below are some steps to help guide that progression and discover the root
cause for the most common classes of issues encountered with the HCP
KubeVirt platform.

## HostedCluster Stuck in Partial State

* Ensure all prerequisites for the HCP KubeVirt platform are met
* Look at conditions on HostedCluster and NodePool objects for validation errors that prevent progress from being made.
* Using the kubeconfig of the guest cluster, inspect the guest cluster’s status. Look at the status of the `oc get clusteroperators` output to see what cluster operators are pending. Look at the node status `oc get nodes` to ensure the worker nodes are in a Ready state.

## HCP has no worker nodes registered

* Look at HostedCluster and NodePool conditions for failures that indicate what the problem could be.
* Look at the KubeVirt worker node VM status for the NodePool. `oc get vm -n <namespace>`
* If the VMs are stuck in provisioning, look at the CDI import pods within the VM’s namespace for clues as to why the importer pods have not completed. `oc get pods -n <namespace> | grep "import"`
* If the VMs are stuck in starting, look to see what the status of the virt-launcher pods are. `oc get pods -n <namespace> -l kubevirt.io=virt-launcher`. If the virt-launcher pods are in a pending state, determine why the pods are not being scheduled. For example, there could be a lack of resources to run the virt-launcher pods.
* If the VMs are running but they have not registered as worker nodes, use the web console to gain VNC access to one of the impacted VMs. The VNC output should indicate that the ignition config was applied. If the VM is unable to access the HCP ignition server on startup, that will prevent the VM from being provisioned correctly.
* If the ignition config has been applied but the VM is still not registering as a node, Proceed to the [Fetching VM Bootstrap logs](#fetching vm bootstrap logs) section to gain access to the VM console logs during boot.

## HCP Worker Nodes Stuck in NotReady State

* During cluster creation, nodes will enter the "NotReady" state temporarily while the networking stack is rolling out. This is a part of normal operation. If this time period takes longer than 10-15 minutes, then it's possible an issue has occurred that needs investigation.
* Look at the conditions on the node object to determine why the node is not ready. `oc get nodes -o yaml`
* Look for failing pods within the cluster `oc get pods -A --field-selector=status.phase!=Running,status.phase!=Succeeded`

## Ingress and Console cluster operators are not coming online

* If the cluster is using the default ingress behavior, ensure that wildcard DNS routes are enabled on the OCP cluster the VMs are hosted on. `oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'`
* If a custom base domain is used for the HCP, double check that the Load Balancer is targeting the VM pods accurately, and make sure the wildcard DNS entry is targeting the Load Balancer IP.

## Guest Cluster Load Balancer services are not becoming available

* Look for events and details associated with the Load Balancer service within the guest cluster
* Load Balancers for the guest cluster are by default handled by the kubevirt-cloud-controller-manager within the HCP namespace. Ensure the kccm pod is online and look at the pod’s logs for errors or warnings. The kccm pod can be identified in the HCP namespace with this command `oc get pods -n <hcp namespace> -l app=cloud-controller-manager`

## Guest Cluster PVCs are not available

* Look for events and details associated with the PVC to understand what errors are occurring
* If the PVC is failing to attach to a Pod, look at the logs for the kubevirt-csi-node daemonset component within the guest to gain further insight into why that might be occurring. The kubevirt-csi-node pods can be identified for each node with this command `oc get pods -n openshift-cluster-csi-drivers -o wide -l app=kubevirt-csi-driver`
* If the PVC is unable to bind to a PV, look at the logs of the kubevirt-csi-controller component within the HCP namespace. The kubevirt-csi-controller pod can be identified within the hcp namespace with this command `oc get pods -n <hcp namespace> -l app=kubevirt-csi-driver`

## Fetching VM bootstrap logs

The VM console logs are really useful to troubleshoot issues when the HyperShift/KubeVirt VM nodes are not correctly joining the cluster.
KubeVirt v1.1.0 will expose logs from the serial console of guest VMs in a k8s native way (`kubectl logs -n <namespace> <vmi_pod> -c guest-console-log`) but his is still not available with KubeVirt v1.0.0.

On KubeVirt v1.0.0 you can use a helper script to stream them from interactive console executed in background.

```bash
#!/bin/bash
HC_NAMESPACE="${HC_NAMESPACE:-clusters}"
NAME=$1

if [[ -z "${NAME}" ]]
then
    echo "Please specify the name of the guest cluster."
    exit 1
fi

VMNS="${HC_NAMESPACE}"-"${NAME}"
REPLICAS=$(oc get NodePool -n "${HC_NAMESPACE}" "${NAME}" -o=jsonpath='{.spec.replicas}')
PLATFORMTYPE=$(oc get NodePool -n "${HC_NAMESPACE}" "${NAME}" -o=jsonpath='{.spec.platform.type}')
INFRAID=$(oc get HostedCluster -n "${HC_NAMESPACE}" "${NAME}" -o=jsonpath='{.spec.infraID}')

if [[ "${PLATFORMTYPE}" != "KubeVirt" ]]; then
    echo "This tool is designed for the KubeVirt provider."
    exit 1
fi

if ! which tmux >/dev/null 2>&1;
then
    echo "this tool requires tmux, please install it."
    exit 1
fi

VMNAMES=()

while [[ ${#VMNAMES[@]} < ${REPLICAS}  ]]; do
  for VMNAME in $(oc get vmi -n "${VMNS}" -l hypershift.openshift.io/infra-id="${INFRAID}" -o name 2>/dev/null); do
    SVMNAME=${VMNAME/virtualmachineinstance.kubevirt.io\//}
    if ! [[ " ${VNMANES[*]} " =~ ${SVMNAME} ]]; then
	   VMNAMES+=("${SVMNAME}")
	   tmux new-session -s "${SVMNAME}" -d "virtctl console --timeout 30 -n ${VMNS} ${SVMNAME} | tee -a ${VMNS}_${SVMNAME}.log"
	   echo "logs for VM ${SVMNAME} will be appended to ${VMNS}_${SVMNAME}.log"
    fi
  done
  sleep 3
done

echo "Log collection will continue in background while the VMs are running."
echo "Please avoid trying to directly connect to VM console with 'virtctl console' to avoid hijacking open sessions:"
echo "you can instead use 'tmux attach -t <vmname>' to reach open session, this will not break file logging."
```

You can locally save the script and execute it as `hypershift_kv_log.sh <guestclustername>` as soon as you created the `hostedcluster` object (the script will not collect logs from the past).
The script will loop until all the expected VMs are created and then log collection will continue in background in `tmux` sessions until the VMs are live.
Please avoid directly connecting to VM console with `virtctl console` to avoid hijacking open sessions braking the logging, you can instead use `tmux attach -t <vmname>` and interactively use the serial console from there.
If the namespace used on the hosted cluster is not named `clusters`, a custom value could be set with the `HC_NAMESPACE` env variable.
