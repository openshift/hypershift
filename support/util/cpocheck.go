package util

import hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

// IsCPOCompatibleWithHCP checks to see if the active image in a control-plane-operator component matches the
// expected image listed in the HCP object
func IsCPOCompatibleWithHCP(hcpAnnotations map[string]string, activeImage string) bool {
	return hcpAnnotations[hyperv1.DesiredControlPlaneOperatorImageAnnotation] == activeImage
}
