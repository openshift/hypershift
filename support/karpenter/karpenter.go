package karpenter

import (
	"context"
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ManagedByKarpenterLabel is a label set on the userData secrets as being managed by Karpenter Operator
	ManagedByKarpenterLabel = "hypershift.openshift.io/managed-by-karpenter"
)

// KarpenterTaintConfigMapName is the name of the configmap containing the karpenter taint config
const KarpenterTaintConfigMapName = "set-karpenter-taint"

// ErrHCPNotFound is returned when no HostedControlPlane is found in the namespace.
var ErrHCPNotFound = errors.New("hostedcontrolplane not found")

// SupportedArchitectures returns the list of supported architectures for Karpenter on the given platform.
func SupportedArchitectures(platform hyperv1.PlatformType) ([]string, error) {
	switch platform {
	case hyperv1.AWSPlatform:
		return []string{hyperv1.ArchitectureAMD64, hyperv1.ArchitectureARM64}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// IsKarpenterEnabled checks if Karpenter is enabled for the given AutoNode configuration.
// Note that we may eventually support other platforms, but for now we only support AWS.
func IsKarpenterEnabled(autoNode *hyperv1.AutoNode) bool {
	return autoNode != nil &&
		autoNode.Provisioner.Name == hyperv1.ProvisionerKarpenter &&
		autoNode.Provisioner.Karpenter != nil &&
		autoNode.Provisioner.Karpenter.Platform == hyperv1.AWSPlatform
}

// GetHCP retrieves the HostedControlPlane from the given namespace.
// Returns ErrHCPNotFound if no HCP exists in the namespace.
func GetHCP(ctx context.Context, c client.Client, namespace string) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := c.List(ctx, hcpList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, fmt.Errorf("%w: namespace %s", ErrHCPNotFound, namespace)
	}
	return &hcpList.Items[0], nil
}

// KarpenterNodePoolName returns the name of the Karpenter NodePool for a given OpenshiftEC2NodeClass
func KarpenterNodePoolName(oec2nc *hyperkarpenterv1.OpenshiftEC2NodeClass) string {
	return fmt.Sprintf("%s-%s", oec2nc.Name, "karpenter")
}

// ArchToAMILabelKey returns a label key to store the AMI ID for the given architecture.
// The label is set on Karpenter userData secrets in order to eventually propagate to EC2NodeClass.AMISelectorTerms.
func ArchToAMILabelKey(arch string) string {
	if arch == hyperv1.ArchitectureAMD64 {
		return hyperkarpenterv1.UserDataAMILabel
	}
	return fmt.Sprintf("%s-%s", hyperkarpenterv1.UserDataAMILabel, arch)
}
