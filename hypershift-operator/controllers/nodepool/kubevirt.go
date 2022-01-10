package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func kubevirtMachineTemplateSpec(nodePool *hyperv1.NodePool) *capikubevirt.KubevirtMachineTemplateSpec {
	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *nodePool.Spec.Platform.Kubevirt.NodeTemplate,
			},
		},
	}
}
