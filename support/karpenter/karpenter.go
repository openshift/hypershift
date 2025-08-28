package karpenter

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// Note that we may eventually support other platforms, but for now we only support AWS.

// IsKarpenterEnabled checks if Karpenter is enabled for the given AutoNode configuration
func IsKarpenterEnabled(autoNode *hyperv1.AutoNode) bool {
	return autoNode != nil &&
		autoNode.Provisioner != nil &&
		autoNode.Provisioner.Name == hyperv1.ProvisionerKarpenter &&
		autoNode.Provisioner.Karpenter != nil &&
		autoNode.Provisioner.Karpenter.Platform == hyperv1.AWSPlatform
}
