# Troubleshooting a KubeVirt cluster

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
