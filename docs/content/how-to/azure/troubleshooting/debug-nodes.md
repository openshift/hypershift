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

## Check kubelet CSR approval

If first-boot configuration completed but no node has registered, check pending
kubelet CSRs in the hosted cluster and the `machine-approver` deployment in the
control plane namespace. A stopped approver or a mismatch between the CSR and
the CAPI Machine identity can block registration. Follow the
[kubelet CSR approval checks](../../../reference/cluster-startup-roadmap.md#kubelet-csr-approval).

## Collect worker diagnostics

To collect Azure worker diagnostics with `hypershift dump cluster`, supply
`--azure-creds` with a credentials file. The Azure identity needs
`Microsoft.Compute/virtualMachines/read` and
`Microsoft.Compute/virtualMachines/retrieveBootDiagnosticsData/action` on the
worker VM resource groups. Enable boot diagnostics before the failure you want
to investigate. In CI, mount the credentials in both test and dump steps; consult
the [platform configuration](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/lifecycle/azure.go)
for the current credential configuration.

Journal collection through the hosted API requires privileged node-log access.
For optional SSH fallback, provision the worker public key at cluster creation
with `--ssh-key` and set `AZURE_JOURNAL_SSH_KEY` to the matching private key file.
Provide port 22 reachability from the collector or a jump host. Configure
jump-host authentication separately and supply trusted host keys; host-key
verification is required. Collection uses existing network access and does not
provision a bastion. See the
[SSH options](https://github.com/openshift/hypershift/blob/main/cmd/consolelogs/azure/journals.go)
for current jump-host and known-hosts configuration.

Check collection warnings when diagnostics are missing. Successful teardown does
not establish that logs were collected. Consult the
[diagnostics collector](https://github.com/openshift/hypershift/tree/main/cmd/consolelogs/azure)
for artifact formats and collection behavior.

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