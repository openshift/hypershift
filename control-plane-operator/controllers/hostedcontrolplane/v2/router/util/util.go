package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"
)

// UseHCPRouter returns true when the HCP routes should be served by a dedicated
// HCP router. This occurs when:
//  1. The cluster is private (e.g. AWS/GCP Private or PublicAndPrivate endpoint access,
//     or ARO with Swift enabled), OR
//  2. The cluster is public but uses a dedicated Route for KAS DNS (rather than a LoadBalancer)
//
// Excludes IBM Cloud platform.
func UseHCPRouter(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return false
	}
	// SharedIngress handles public routing for ARO-HCP; a dedicated HCP
	// router is only needed when the cluster also has private access.
	if util.UseSharedIngressHCP(hcp) && !util.IsPrivateHCP(hcp) {
		return false
	}
	// Router infrastructure is needed when:
	// 1. Cluster has private access (Private or PublicAndPrivate) - for internal routes, OR
	// 2. External routes are labeled for HCP router (Public with KAS DNS)
	return util.IsPrivateHCP(hcp) || util.LabelHCPRoutes(hcp)
}
