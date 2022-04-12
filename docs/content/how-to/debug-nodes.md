# Debug why nodes have not joined the cluster

If your control plane API endpoint has become available, but nodes are not joining the hosted cluster,
you can check the following:

1. Check that your machines have been created in the control plane namespace:
   ```
   HC_NAMESPACE="clusters"
   HC_NAME="cluster-name"
   CONTROL_PLANE_NAMESPACE="${HC_NAMESPACE}-${HC_NAME}"
   oc get machines.cluster.x-k8s.io -n $CONTROL_PLANE_NAMESPACE
   oc get awsmachines -n $CONTROL_PLANE_NAMESPACE
   ```

    If machines don't exist, check that a machinedeployment and machineset have been created:
    ```
    oc get machinedeployment -n $CONTROL_PLANE_NAMESPACE
    oc get machineset -n $CONTROL_PLANE_NAMESPACE
    ```
    In the case that no machinedeployment has been created look at the logs of the hypershift 
    operator:
    ```
    oc logs deployment/operator -n hypershift
    ```

    If the machines exist but have not been provisioned, check the log of the cluster API provider:
    ```
    oc logs deployment/capi-provider -c manager -n $CONTROL_PLANE_NAMESPACE
    ```

2. If machines exist and have been provisioned, check that they have been initialized via ignition.
   If you are using AWS, you can look at the system console logs of the machines by using the hypershift
   console-logs utility:

    ```
    ./bin/hypershift console-logs aws --name $HC_NAME --aws-creds ~/.aws/credentials --output-dir /tmp/console-logs
    ```
   
    The console logs will be placed in the destination directory.
    When looking at the console logs look for any errors accessing the ignition endpoint via https. If there are,
    then issue is somehow the ignition endpoint exposed by the control plane is not accessible from the worker
    instances.

3. If the machines look like they have been provisioned correctly, you can access the systemd journal of
   each machine. You can do this by first creating a bastion machine that will allow you to ssh to worker nodes
   and running a utility script that will download logs from the machines.

    Extract the public/private key for the cluster. If you created the cluster with the --generate-ssh flag, a
    ssh key for your cluster was placed in the same namespace as the hosted cluster (default `clusters`). If you 
    specified your own key and know how to access it, you can skip this step.
    ```
    mkdir /tmp/ssh
    oc get secret -n clusters ${NAME}-ssh-key -o jsonpath='{ .data.id_rsa }' | base64 -d > /tmp/ssh/id_rsa
    oc get secret -n clusters ${NAME}-ssh-key -o jsonpath='{ .data.id_rsa\.pub }' | base64 -d > /tmp/ssh/id_rsa.pub
    ```
 
    Create a bastion machine
    ```
    ./bin/hypershift create bastion aws --aws-creds ~/.aws/credentials --name $CLUSTER_NAME --ssh-key-file /tmp/ssh/id_rsa.pub
    ```
 
    Run the following script to extract journals from each of your workers:
    ```
    mkdir /tmp/journals
    INFRAID="$(oc get hc -n clusters $CLUSTER_NAME -o jsonpath='{ .spec.infraID }')"
    SSH_PRIVATE_KEY=/tmp/ssh/id_rsa
    ./test/e2e/util/dump/copy-machine-journals.sh /tmp/journals
    ```
 
    Machine journals should be placed in the `/tmp/journals` directory in compressed format. Extract them and look for a repeating
    error near the bottom that should indicate why the kubelet has not been able to join the cluster.