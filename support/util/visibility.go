package util

import (
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

const managedServiceEnvVar = "MANAGED_SERVICE"

func isAroHCP() bool {
	return os.Getenv(managedServiceEnvVar) == hyperv1.AroHCP
}

func IsPrivateHCP(hcp *hyperv1.HostedControlPlane) bool {
	// ARO always have swift enabled.
	// We still check the annotation to keep CI working.
	// TODO(alberto): Remove this once CI has swift enabled.
	if isAroHCP() && hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		return true
	}
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}

	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hcp.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	return false
}

func IsPublicHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Public
	}
	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hcp.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate
	}
	return true
}

func IsPrivateHC(hc *hyperv1.HostedCluster) bool {
	// ARO always have swift enabled.
	// We still check the annotation to keep CI working.
	// TODO(alberto): Remove this once CI has swift enabled.
	if isAroHCP() && hc.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		return true
	}
	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}
	if hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hc.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	return false
}

func IsPublicHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Public
	}
	if hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hc.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate
	}
	return true
}

// LabelHCPRoutes determines if routes should be labeled for admission by the HCP router.
// Routes with the label "hypershift.openshift.io/hosted-control-plane" are served by a
// dedicated HCP router (HAProxy deployment in the HCP namespace). Routes without this label
// are served by the management cluster's default OpenShift ingress controller.
//
// This function is the single source of truth for route labeling decisions and is called by:
// - OAuth route reconciliation (external public/private routes)
// - Konnectivity route reconciliation (external routes)
// - Ignition server route reconciliation (external routes)
// - Router component predicate (determines if router Deployment/ConfigMap/PDB are created)
// - Router service creation (determines if public router LoadBalancer service is created)
//
// The HCP router infrastructure (Deployment, Services) is created when routes need to be labeled.
// This ensures routes and router services stay synchronized.
//
// # Platform-Specific Behavior
//
// AWS Platform:
//   - Private: Always labels routes (no public access)
//   - PublicAndPrivate + KAS LoadBalancer: Does NOT label external routes (uses mgmt cluster router)
//   - PublicAndPrivate + KAS Route: Labels routes (uses HCP router for all routes)
//   - Public + KAS LoadBalancer: Does NOT label routes (uses mgmt cluster router)
//   - Public + KAS Route: Labels routes (uses HCP router)
//
// GCP Platform:
//   - Same behavior as AWS platform
//
// Agent Platform (bare metal):
//   - No EndpointAccess field (no Private/PublicAndPrivate concept)
//   - Labels routes ONLY when KAS uses Route with explicit hostname
//   - KAS LoadBalancer/NodePort: Does NOT label routes (uses mgmt cluster router)
//
// KubeVirt, OpenStack, None Platforms:
//   - Same behavior as Agent platform
//   - Labels routes ONLY when KAS uses Route with explicit hostname
//
// IBM Cloud Platform:
//   - Never labels routes (uses different routing mechanism)
//
// # Internal Routes
//
// Note that internal routes (*.apps.<cluster>.hypershift.local) are ALWAYS labeled for
// HCP router regardless of this function's return value. This function only controls
// EXTERNAL route labeling. Internal routes are handled separately in ReconcileInternalRoute().
//
// # Architecture Reference
//
// For complete details on the HCP ingress architecture, see HCP_INGRESS_ARCHITECTURE.md
// in the repository root, which documents the full decision flow, code references, and
// interaction between route labeling and router service creation.
//
// Returns true when routes should be labeled for HCP router; false when routes should
// use the management cluster router.
func LabelHCPRoutes(hcp *hyperv1.HostedControlPlane) bool {
	// When shared ingress is active (e.g., ARO HCP), all routes must be labeled
	// so the SharedIngressReconciler can discover and admit them.
	if isAroHCP() {
		return true
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform, hyperv1.GCPPlatform:
		// AWS and GCP support endpoint access modes (Private/PublicAndPrivate/Public).
		// Label routes for HCP router when:
		// 1. Cluster has no public access (Private-only), OR
		// 2. Public cluster with dedicated DNS for KAS (KAS uses Route with hostname)
		//
		// For PublicAndPrivate clusters using LoadBalancer for KAS:
		// - Internal routes (Konnectivity, Ignition) are served by internal HCP router
		// - External routes (OAuth) use the management cluster router (no public HCP router needed)
		// This avoids creating an unnecessary public LoadBalancer service.
		return !IsPublicHCP(hcp) || UseDedicatedDNSForKAS(hcp)

	case hyperv1.AzurePlatform, hyperv1.AgentPlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform, hyperv1.NonePlatform:
		// These platforms do not have endpoint access mode concepts (no Private/PublicAndPrivate).
		// Label routes for HCP router ONLY when KAS explicitly uses Route with a hostname.
		//
		// This prevents creating HCP router infrastructure when:
		// - KAS uses LoadBalancer (routes should use management cluster router)
		// - KAS uses NodePort (routes should use management cluster router)
		//
		// When KAS uses Route with hostname, all routes are labeled for HCP router to ensure
		// consistent routing through dedicated infrastructure.
		return UseDedicatedDNSForKAS(hcp)

	case hyperv1.IBMCloudPlatform:
		// IBM Cloud uses a different routing mechanism (shared ingress with HAProxy and
		// kube-apiserver-proxy on worker nodes). Never use HCP router.
		return false

	case hyperv1.PowerVSPlatform:
		// PowerVS (IBM Cloud Power Virtual Servers) follows the same pattern as other
		// platforms without endpoint access modes.
		return UseDedicatedDNSForKAS(hcp)

	default:
		// Conservative default for unknown platforms: do not create HCP router infrastructure.
		// Routes will use the management cluster router.
		return false
	}
}
