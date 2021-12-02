package nodepool

import (
	"fmt"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func AgentMachineTemplate(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *agentv1.AgentMachineTemplate {
	agentMachineTemplate := &agentv1.AgentMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodePoolAnnotation: ctrlclient.ObjectKeyFromObject(nodePool).String(),
			},
			Namespace: controlPlaneNamespace,
		},
		Spec: agentv1.AgentMachineTemplateSpec{
			Template: agentv1.AgentMachineTemplateResource{
				Spec: agentv1.AgentMachineSpec{},
			},
		},
	}
	specHash := hashStruct(agentMachineTemplate.Spec.Template.Spec)
	agentMachineTemplate.SetName(fmt.Sprintf("%s-%s", nodePool.GetName(), specHash))

	return agentMachineTemplate
}
