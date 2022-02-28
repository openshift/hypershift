package nodepool

import (
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func agentMachineTemplateSpec(nodePool *hyperv1.NodePool) *agentv1.AgentMachineTemplateSpec {
	spec := agentv1.AgentMachineSpec{}
	if nodePool.Spec.Platform.Agent != nil {
		spec.AgentLabelSelector = nodePool.Spec.Platform.Agent.AgentLabelSelector
	}
	return &agentv1.AgentMachineTemplateSpec{
		Template: agentv1.AgentMachineTemplateResource{
			Spec: spec,
		},
	}
}
