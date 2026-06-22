package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
)

// validOSImageStreamCondition validates spec.osImageStream and sets the
// ValidOSImageStream condition on the NodePool.
func (r *NodePoolReconciler) validOSImageStreamCondition(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster) (*ctrl.Result, error) {
	if nodePool.Spec.OSImageStream.Name == "" {
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidOSImageStreamConditionType)
		return nil, nil
	}

	name := nodePool.Spec.OSImageStream.Name
	if name != "rhel-9" && name != "rhel-10" {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidOSImageStreamConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolInvalidOSImageStreamReason,
			Message:            fmt.Sprintf("Unsupported OS image stream %q; must be one of: rhel-9, rhel-10", name),
			ObservedGeneration: nodePool.Generation,
		})
		return nil, nil
	}

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidOSImageStreamConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            fmt.Sprintf("Using OS image stream %q", name),
		ObservedGeneration: nodePool.Generation,
	})
	return nil, nil
}
