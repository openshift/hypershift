Now it's a matter of waiting for the cluster to finish the deployment, so let's take a look at some useful commands on the Management cluster side:

```bash
export KUBECONFIG=/root/.kcli/clusters/hub-ipv4/auth/kubeconfig

watch "oc get pod -n hypershift;echo;echo;oc get pod -n clusters-hosted-ipv4;echo;echo;oc get bmh -A;echo;echo;oc get agent -A;echo;echo;oc get infraenv -A;echo;echo;oc get hostedcluster -A;echo;echo;oc get nodepool -A;echo;echo;"
```

This command will give you info about:

- What is the Hypershift Operator status
- The HostedControlPlane pod status
- The BareMetalHosts
- The Agents
- The Infraenv
- The HostedCluster and NodePool

This is how it looks:

![img](/images/watch-cp.png)