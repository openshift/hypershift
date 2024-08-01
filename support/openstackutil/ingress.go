package openstackutil

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// ValidateIngressOptions checks that the OpenStack ingress provider is valid and that the floating IP is set if the provider is "octavia".
func ValidateIngressOptions(ingressProvider hyperv1.OpenStackIngressProvider, ingressFloatingIP string) error {
	if ingressFloatingIP == "" && ingressProvider == "" {
		return nil
	}
	if ingressFloatingIP != "" && ingressProvider == "" {
		return fmt.Errorf("cannot set floating IP without specifying ingress provider")
	}
	if ingressProvider != "" && ingressFloatingIP == "" {
		return fmt.Errorf("cannot set ingress provider without specifying floating IP")
	}
	// For now, the floating IP can only be set when the ingress provider is "Octavia".
	// This is because the floating IP is only used for the Octavia ingress provider.
	if ingressProvider != "" && ingressProvider != hyperv1.OpenStackIngressProviderOctavia {
		return fmt.Errorf("invalid ingress provider %s", ingressProvider.String())
	}
	return nil
}
