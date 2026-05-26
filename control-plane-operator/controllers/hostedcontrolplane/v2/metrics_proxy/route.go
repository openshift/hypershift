package metricsproxy

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"

	routev1 "github.com/openshift/api/route/v1"
)

func (mp *metricsProxy) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	hcp := cpContext.HCP
	serviceName := ComponentName

	if netutil.IsPrivateHCP(hcp) {
		return netutil.ReconcileInternalRoute(route, hcp.Name, serviceName)
	}

	// Derive hostname from the Ignition route's domain when an explicit hostname
	// is configured. On platforms using External DNS (e.g. Azure), the CLI sets
	// explicit hostnames on service publishing strategies. Since metrics-proxy has
	// no strategy entry, derive from the Ignition strategy's domain.
	hostname := ""
	ignitionStrategy := netutil.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Ignition)
	if ignitionStrategy != nil && ignitionStrategy.Route != nil && ignitionStrategy.Route.Hostname != "" {
		parts := strings.SplitN(ignitionStrategy.Route.Hostname, ".", 2)
		if len(parts) == 2 {
			hostname = fmt.Sprintf("metrics-proxy-%s.%s", hcp.Name, parts[1])
		}
	}

	labelHCPRoutes := netutil.LabelHCPRoutes(hcp)
	if err := netutil.ReconcileExternalRoute(route, hostname, mp.defaultIngressDomain, serviceName, labelHCPRoutes); err != nil {
		return err
	}

	// When using ApplyManifest (component framework), we need to explicitly mark labels
	// for removal because preserveOriginalMetadata merges labels instead of replacing them.
	// ReconcileExternalRoute works on the manifest object (no existing label), so we mark
	// it for removal here to ensure preserveOriginalMetadata deletes it from the cluster object.
	if !labelHCPRoutes {
		netutil.MarkHCPRouteLabelForRemoval(route)
	}

	return nil
}
