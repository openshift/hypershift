package netutil

import (
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const managedServiceEnvVar = "MANAGED_SERVICE"

func isAroHCP() bool {
	return os.Getenv(managedServiceEnvVar) == hyperv1.AroHCP
}

// UseSwiftNetworkingHCP returns true when the HCP uses Azure Swift pod networking.
// Checks the Private.Type API field first; falls back to env var + annotation
// for unmigrated clusters during the Phase 1 migration window.
func UseSwiftNetworkingHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type != hyperv1.AzurePlatform {
		return false
	}
	azure := ptr.Deref(hcp.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{})
	if azure.Private.Type == hyperv1.AzurePrivateTypeSwift {
		return true
	}
	if isAroHCP() && hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		klog.V(2).Info("Using legacy annotation fallback for Swift networking detection on HostedControlPlane", "namespace", hcp.Namespace, "name", hcp.Name)
		return true
	}
	return false
}

// UseSwiftNetworkingHC returns true when the HostedCluster uses Azure Swift pod networking.
func UseSwiftNetworkingHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return false
	}
	azure := ptr.Deref(hc.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{})
	if azure.Private.Type == hyperv1.AzurePrivateTypeSwift {
		return true
	}
	if isAroHCP() && hc.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation] != "" {
		klog.V(2).Info("Using legacy annotation fallback for Swift networking detection on HostedCluster", "namespace", hc.Namespace, "name", hc.Name)
		return true
	}
	return false
}

// IsAroHCPByHCP returns true when this HCP belongs to an ARO-managed cluster.
// Checks AzureAuthenticationConfigType == ManagedIdentities as the per-cluster
// API indicator. Falls back to the MANAGED_SERVICE env var when the Azure spec
// is nil (unmigrated clusters during Phase 1).
func IsAroHCPByHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type != hyperv1.AzurePlatform {
		return false
	}
	azure := hcp.Spec.Platform.Azure
	if azure != nil {
		return azure.AzureAuthenticationConfig.AzureAuthenticationConfigType == hyperv1.AzureAuthenticationTypeManagedIdentities
	}
	return isAroHCP()
}

// IsAroHCPByHC returns true when this HostedCluster belongs to an ARO-managed cluster.
func IsAroHCPByHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return false
	}
	azure := hc.Spec.Platform.Azure
	if azure != nil {
		return azure.AzureAuthenticationConfig.AzureAuthenticationConfigType == hyperv1.AzureAuthenticationTypeManagedIdentities
	}
	return isAroHCP()
}

// UseSharedIngressHCP returns true when this specific HCP should use the
// management cluster's shared ingress (HAProxy) for public endpoints.
// IsAroHCPByHCP includes an env var fallback for unmigrated clusters.
func UseSharedIngressHCP(hcp *hyperv1.HostedControlPlane) bool {
	return IsAroHCPByHCP(hcp) && IsPublicHCP(hcp)
}

// UseSharedIngressHC returns true when this specific HostedCluster should use
// the management cluster's shared ingress for public endpoints.
func UseSharedIngressHC(hc *hyperv1.HostedCluster) bool {
	return IsAroHCPByHC(hc) && IsPublicHC(hc)
}

// SwiftPodNetworkInstanceHCP returns the Swift pod network instance name for
// the HCP. Checks the API field first; falls back to the annotation for
// unmigrated clusters.
func SwiftPodNetworkInstanceHCP(hcp *hyperv1.HostedControlPlane) string {
	if hcp.Spec.Platform.Type != hyperv1.AzurePlatform {
		return ""
	}
	azure := ptr.Deref(hcp.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{})
	if azure.Private.Type == hyperv1.AzurePrivateTypeSwift {
		return azure.Private.Swift.PodNetworkInstance
	}
	return hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation]
}

func IsPrivateHCP(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hcp.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}
	if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hcp.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	if hcp.Spec.Platform.Type == hyperv1.AzurePlatform {
		topology := ptr.Deref(hcp.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{}).Topology
		if topology != "" {
			return topology == hyperv1.AzureTopologyPublicAndPrivate || topology == hyperv1.AzureTopologyPrivate
		}
		// Phase 1 fallback: unmigrated clusters with empty topology
		return UseSwiftNetworkingHCP(hcp)
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
	if hcp.Spec.Platform.Type == hyperv1.AzurePlatform {
		topology := ptr.Deref(hcp.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{}).Topology
		return topology == hyperv1.AzureTopologyPublicAndPrivate || topology == hyperv1.AzureTopologyPublic || topology == ""
	}
	return true
}

func IsPrivateHC(hc *hyperv1.HostedCluster) bool {
	if hc.Spec.Platform.Type == hyperv1.AWSPlatform {
		access := ptr.Deref(hc.Spec.Platform.AWS, hyperv1.AWSPlatformSpec{}).EndpointAccess
		return access == hyperv1.PublicAndPrivate || access == hyperv1.Private
	}
	if hc.Spec.Platform.Type == hyperv1.GCPPlatform {
		access := ptr.Deref(hc.Spec.Platform.GCP, hyperv1.GCPPlatformSpec{}).EndpointAccess
		return access == hyperv1.GCPEndpointAccessPublicAndPrivate || access == hyperv1.GCPEndpointAccessPrivate
	}
	if hc.Spec.Platform.Type == hyperv1.AzurePlatform {
		topology := ptr.Deref(hc.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{}).Topology
		if topology != "" {
			return topology == hyperv1.AzureTopologyPublicAndPrivate || topology == hyperv1.AzureTopologyPrivate
		}
		// Phase 1 fallback: unmigrated clusters with empty topology
		return UseSwiftNetworkingHC(hc)
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
	if hc.Spec.Platform.Type == hyperv1.AzurePlatform {
		topology := ptr.Deref(hc.Spec.Platform.Azure, hyperv1.AzurePlatformSpec{}).Topology
		return topology == hyperv1.AzureTopologyPublicAndPrivate || topology == hyperv1.AzureTopologyPublic || topology == ""
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
// Azure Platform:
//   - Same behavior as AWS platform (supports endpoint access modes)
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
	// When shared ingress or Swift networking is active, all routes must be
	// labeled so the SharedIngressReconciler can discover them and the HCP
	// router can serve them.
	if UseSharedIngressHCP(hcp) || UseSwiftNetworkingHCP(hcp) {
		return true
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform, hyperv1.GCPPlatform, hyperv1.AzurePlatform:
		// AWS, GCP, and Azure support endpoint access modes (Private/PublicAndPrivate/Public).
		// Label routes for HCP router when:
		// 1. Cluster has no public access (Private-only), OR
		// 2. Public cluster with dedicated DNS for KAS (KAS uses Route with hostname)
		//
		// For PublicAndPrivate clusters using LoadBalancer for KAS:
		// - Internal routes (Konnectivity, Ignition) are served by internal HCP router
		// - External routes (OAuth) use the management cluster router (no public HCP router needed)
		// This avoids creating an unnecessary public LoadBalancer service.
		return !IsPublicHCP(hcp) || UseDedicatedDNSForKAS(hcp)

	case hyperv1.AgentPlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform, hyperv1.NonePlatform:
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
