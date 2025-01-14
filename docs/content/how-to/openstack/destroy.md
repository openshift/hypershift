# Delete a HostedCluster on OpenStack

To delete a HostedCluster on OpenStack:

```shell
hcp destroy cluster openstack --name $CLUSTER_NAME
```

The process will take a few minutes to complete and will destroy all resources associated with the HostedCluster including OpenStack resources such as servers, networks, etc.
