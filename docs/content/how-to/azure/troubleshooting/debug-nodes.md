# Debug Why Azure Nodes Have Not Joined
If your control plane API endpoint has become available, but the nodes have not joined the hosted cluster, check the following:

## Verify machines were created
```
HC_NAMESPACE="clusters"
HC_NAME="cluster-name"
CONTROL_PLANE_NAMESPACE="${HC_NAMESPACE}-${HC_NAME}"
oc get machines.cluster.x-k8s.io -n $CONTROL_PLANE_NAMESPACE
oc get azuremachines.infrastructure.cluster.x-k8s.io -n $CONTROL_PLANE_NAMESPACE
```

If machines don't exist, check that a machinedeployment and machineset have been created:
```
oc get machinedeployment -n $CONTROL_PLANE_NAMESPACE
oc get machineset -n $CONTROL_PLANE_NAMESPACE
```

In the case that no machinedeployment was created, look at the logs of the hypershift 
operator:
```
oc logs -l app=operator -n hypershift --tail=$NUMBER_OF_LINES
```

If the machines exist but have not been provisioned, check the log of the cluster API provider:
```
oc logs deployment/capi-provider -c manager -n $CONTROL_PLANE_NAMESPACE
```

## Create a bastion to SSH to a node
If the machines look like they have been provisioned correctly, you can directly access the virtual machines related to your nodes through a bastion. 

### Prerequisites 
- Download the `az` cli
- Add the following extensions to the cli:
  - `az extension update --name bastionaz extension update --name bastion`
  - `az extension add -n ssh`
- Extract the private key for the cluster. If you created the cluster with the --generate-ssh flag, a
  ssh key for your cluster was placed in the same namespace as the hosted cluster (default `clusters`). If you
  specified your own key and know how to access it, you can skip this step.
  - `oc get secret -n clusters ${HC_NAME}-ssh-key -o jsonpath='{ .data.id_rsa }' | base64 -d > /tmp/ssh/id_rsa`
- Set the permissions on the key `chmod 400 /tmp/ssh/id_rsa`

### Create a bastion machine
1. Log into the Azure Portal and go to the resource group where your virtual machine was created
2. Click on the `Connect` button then `Connect to Bastion`
3. Accept the defaults and click `Deploy Bastion`
4. Once the bastion and its related resources are created, you will need to modify some of its configuration
   1. Click on the bastion resource
   2. Click Configuration
   3. Set the Tier to `Standard` and check `Native client support`
5. Wait for the new settings to take effect
6. Log into the bastion via terminal through this command `az login --scope https://management.core.windows.net//.defaultnetwork bastion ssh --name <bastion-name> --resource-group <resource-group-name> --target-resource-id <vm-id> --auth-type ssh-key --username core --ssh-key <path-to-your-rsa-secret>`
7. You should now be able to access various directories and logs to debug why the machine did not join
   1. One suggestion would be to look at the journal logs and look for a repeating error near the bottom that should indicate why the kubelet has not been able to join the cluster: `sudo journalctl`