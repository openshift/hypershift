package nodepool

import (
	"fmt"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *CAPI) agentMachineTemplate(templateNameGenerator func(spec any) (string, error)) (*agentv1.AgentMachineTemplate, error) {
	spec := agentv1.AgentMachineTemplateSpec{}
	if c.nodePool.Spec.Platform.Agent != nil {
		spec.Template.Spec.AgentLabelSelector = c.nodePool.Spec.Platform.Agent.AgentLabelSelector
	}

	templateName, err := templateNameGenerator(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate template name: %w", err)
	}

	template := &agentv1.AgentMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: templateName,
		},
		Spec: spec,
	}

	return template, nil
}
