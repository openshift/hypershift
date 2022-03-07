package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/kubevirt"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func kubevirtMachineTemplateSpec(nodePool *hyperv1.NodePool) (*capikubevirt.KubevirtMachineTemplateSpec, error) {
	nodeTemplate, err := kubevirt.RawExtensionToVirtualMachineTemplateSpec(nodePool.Spec.Platform.Kubevirt.NodeTemplate)
	if err != nil {
		return nil, err
	}
	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *nodeTemplate,
			},
		},
	}, nil
}
