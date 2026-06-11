package util

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
func LabelHCPRoutes(hcp *hyperv1.HostedControlPlane) bool {
	// When shared ingress or Swift networking is active, all routes must be
	// labeled so the SharedIngressReconciler can discover them and the HCP
	// router can serve them.
	if UseSharedIngressHCP(hcp) || UseSwiftNetworkingHCP(hcp) {
		return true
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform, hyperv1.GCPPlatform, hyperv1.AzurePlatform:
		return !IsPublicHCP(hcp) || UseDedicatedDNSForKAS(hcp)

	case hyperv1.AgentPlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform, hyperv1.NonePlatform:
		return UseDedicatedDNSForKAS(hcp)

	case hyperv1.IBMCloudPlatform:
		return false

	case hyperv1.PowerVSPlatform:
		return UseDedicatedDNSForKAS(hcp)

	default:
		return false
	}
}
