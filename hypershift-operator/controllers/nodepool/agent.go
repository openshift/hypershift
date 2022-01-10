package nodepool

import (
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
)

func agentMachineTemplateSpec() *agentv1.AgentMachineTemplateSpec {
	return &agentv1.AgentMachineTemplateSpec{
		Template: agentv1.AgentMachineTemplateResource{
			Spec: agentv1.AgentMachineSpec{},
		},
	}
}
