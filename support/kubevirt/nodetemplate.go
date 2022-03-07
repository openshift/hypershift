package kubevirt

import (
	"github.com/openshift/hypershift/support/api"
	"k8s.io/apimachinery/pkg/runtime"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func VirtualMachineTemplateSpecToRawExtension(vmTemplateSpec *capikubevirt.VirtualMachineTemplateSpec) *runtime.RawExtension {
	if vmTemplateSpec == nil {
		return nil
	}
	return &runtime.RawExtension{
		Object: &capikubevirt.KubevirtMachine{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *vmTemplateSpec,
			},
		},
	}
}

func RawExtensionToVirtualMachineTemplateSpec(rawExt *runtime.RawExtension) (*capikubevirt.VirtualMachineTemplateSpec, error) {
	if rawExt == nil {
		return nil, nil
	}
	gvk := capikubevirt.GroupVersion.WithKind("KubevirtMachine")
	virtMachine := &capikubevirt.KubevirtMachine{}
	if _, _, err := api.JsonSerializer.Decode(rawExt.Raw, &gvk, virtMachine); err != nil {
		return nil, err
	}
	return &virtMachine.Spec.VirtualMachineTemplate, nil
}
