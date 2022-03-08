# Agent Platform

Use the Agent platform for installing platform-agnostic worker nodes.

## Prerequisites

Ensure that you have installed the Infrastructure Operator on the management cluster. You may install it via RHACM, OLM, or by following [these](https://github.com/openshift/assisted-service/blob/master/docs/operator.md) instructions.

## Setup

The Agent platform uses the [Infrastructure Operator](https://github.com/openshift/assisted-service) (AKA Assisted Installer) to add worker nodes to a hosted cluster. For a primer on the Infrastructure Operator, see [here](https://github.com/openshift/assisted-service/blob/master/docs/hive-integration/kube-api-getting-started.md).

First, create one or more InfraEnv CRs, specifying any necessary properties. Download the Discovery Image according to the URL in the InfraEnv’s Status and boot hosts with it.

Each host will automatically run an agent process that will register with the Infrastructure Operator, creating a corresponding Agent CR.

At this point, you may optionally set the hostname, installation disk, and other parameters in the Agent’s Spec. Approve the Agent once you are satisfied that the properties are correct and that you recognize the host.

## HyperShift flow

When you create a HostedCluster with the Agent platform, HyperShift will install the [Agent CAPI provider](https://github.com/openshift/cluster-api-provider-agent) in the HyperShift control plane namespace namespace.

Upon scaling up a NodePool, a Machine will be created, and the CAPI provider will find a suitable Agent to match this Machine. Suitable means that the Agent is approved, is passing validations, is not currently bound (in use), and has the requirements specified on the NodePool Spec (e.g., minimum CPU/RAM, labels matching the label selector). You may monitor the installation of an Agent by checking its Status and Conditions.

Upon scaling down a NodePool, Agents will be unbound from the corresponding cluster. However, you must boot them with the Discovery Image once again before reusing them.

## Try it out
* Create a HostedCluster with the Agent platform:
  ```sh
  hypershift create cluster agent --pull-secret {path_to_pull_secret_file} --name {cluster_name} --agent-namespace {agent_namespace}
  ```
  (agent_namespace is the namespace where the Agent CRs reside)
* Scale up the Nodepool:
	* Optionally specify which Agents to choose:
		* NodePool.Spec.AgentLabelSelector (labels that must be set on an Agent in order to be selected)
	* Set NodePool.Spec.NodeCount to the desired number of nodes