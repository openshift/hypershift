If you check the Hosted cluster side you can check how the Operators are progressing and what is the status. To do that we will use these commands

```bash
oc get secret -n clusters-hosted-ipv4 admin-kubeconfig -o jsonpath='{.data.kubeconfig}' |base64 -d > /root/hc_admin_kubeconfig.yaml
export KUBECONFIG=/root/hc_admin_kubeconfig.yaml

watch "oc get clusterversion,nodes,co"
```

This command will give you info about:

- Check the clusterversion
- Check if the Nodes has joined the cluster
- Check the ClusterOperators

This is how looks like:

![img](/images/watch-dp.png)