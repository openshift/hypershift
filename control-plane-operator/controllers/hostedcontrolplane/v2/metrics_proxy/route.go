package metricsproxy

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"
)

func (mp *metricsProxy) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	hcp := cpContext.HCP
	serviceName := ComponentName

	if util.IsPrivateHCP(hcp) {
		return util.ReconcileInternalRoute(route, hcp.Name, serviceName)
	}

	labelHCPRoutes := util.LabelHCPRoutes(hcp)
	if err := util.ReconcileExternalRoute(route, "", mp.defaultIngressDomain, serviceName, labelHCPRoutes); err != nil {
		return err
	}

	// When using ApplyManifest (component framework), we need to explicitly mark labels
	// for removal because preserveOriginalMetadata merges labels instead of replacing them.
	// ReconcileExternalRoute works on the manifest object (no existing label), so we mark
	// it for removal here to ensure preserveOriginalMetadata deletes it from the cluster object.
	if !labelHCPRoutes {
		util.MarkHCPRouteLabelForRemoval(route)
	}

	return nil
}
