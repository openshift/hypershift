package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
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
