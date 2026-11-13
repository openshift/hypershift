package hostedcluster

import (
	"context"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiconversion "sigs.k8s.io/cluster-api/core/webhooks/conversion"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

var capiMachineConverter = conversion.NewHubSpokeConverter(&capiv1beta2.Machine{},
	conversion.NewSpokeConverter(&capiv1beta1.Machine{}, capiconversion.ConvertMachineHubToV1Beta1, convertCAPIMachineV1Beta1ToHub),
)

func convertCAPIMachineV1Beta1ToHub(ctx context.Context, src *capiv1beta1.Machine, dst *capiv1beta2.Machine) error {
	if err := capiconversion.ConvertMachineV1Beta1ToHub(ctx, src, dst); err != nil {
		return err
	}

	projectedStatus := capiv1beta1.MachineStatus{}
	if err := capiv1beta1.Convert_v1beta2_MachineStatus_To_v1beta1_MachineStatus(&dst.Status, &projectedStatus, nil); err != nil {
		return err
	}

	// Older hosted controllers write the legacy phase; preserve hub Updating when its legacy Running projection is unchanged.
	if src.Status.Phase != projectedStatus.Phase {
		dst.Status.Phase = src.Status.Phase
	}
	return nil
}
