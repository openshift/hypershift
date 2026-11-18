package nodepool

import hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

// isInterruptibleInstanceEnabled determines if a NodePool creates instances
// that can be interrupted by the cloud provider with advance notice.
func isInterruptibleInstanceEnabled(nodePool *hyperv1.NodePool) bool {
	if nodePool == nil {
		return false
	}

	if nodePool.Annotations != nil {
		if _, ok := nodePool.Annotations[AnnotationEnableSpot]; ok {
			return true
		}
	}

	if nodePool.Spec.Platform.AWS != nil &&
		nodePool.Spec.Platform.AWS.Placement != nil &&
		nodePool.Spec.Platform.AWS.Placement.MarketType == hyperv1.MarketTypeSpot {
		return true
	}

	if nodePool.Spec.Platform.GCP != nil {
		switch nodePool.Spec.Platform.GCP.ProvisioningModel {
		case hyperv1.GCPProvisioningModelSpot, hyperv1.GCPProvisioningModelPreemptible:
			return true
		}
	}

	return false
}
