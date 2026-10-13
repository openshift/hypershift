The agent platform does not create any infrastructure but does have two kinds of prerequisites:

1. Agents: An Agent represents a host booted with a discovery image and ready to be provisioned as an OpenShift node. For more information, see [here](https://github.com/openshift/assisted-service/blob/master/docs/hive-integration/kube-api-getting-started.md).
1. DNS: The API and ingress endpoints must be routable.

You can find more details about the prerequisites in the [how-to](../../how-to/agent/create-agent-cluster.md).
