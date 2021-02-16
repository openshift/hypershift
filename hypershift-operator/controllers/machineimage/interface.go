package machineimage

import hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

// ImageProvider provides a cloud image to use for a given HostedCluster
type Provider interface {
	Image(cluster *hyperv1.HostedCluster) (string, error)
}
