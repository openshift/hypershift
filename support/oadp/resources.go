package oadp

import "strings"

var (
	// Base resources common to all platforms
	BaseResources = []string{
		"serviceaccounts", "roles", "rolebindings", "pods", "persistentvolumeclaims", "persistentvolumes", "configmaps",
		"priorityclasses", "poddisruptionbudgets", "hostedclusters.hypershift.openshift.io", "nodepools.hypershift.openshift.io",
		"secrets", "services", "deployments", "statefulsets",
		"hostedcontrolplanes.hypershift.openshift.io", "clusters.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io", "machinesets.cluster.x-k8s.io", "machines.cluster.x-k8s.io",
		"routes.route.openshift.io", "clusterdeployments.hive.openshift.io",
	}

	// Platform-specific resources constants
	AWSResources = []string{
		"awsclusters.infrastructure.cluster.x-k8s.io", "awsmachinetemplates.infrastructure.cluster.x-k8s.io", "awsmachines.infrastructure.cluster.x-k8s.io",
	}
	AgentResources = []string{
		"agentclusters.infrastructure.cluster.x-k8s.io", "agentmachinetemplates.infrastructure.cluster.x-k8s.io", "agentmachines.infrastructure.cluster.x-k8s.io",
		"agents.agent-install.openshift.io", "infraenvs.agent-install.openshift.io", "baremetalhosts.metal3.io",
	}
	KubevirtResources = []string{
		"kubevirtclusters.infrastructure.cluster.x-k8s.io", "kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io",
	}
	OpenstackResources = []string{
		"openstackclusters.infrastructure.cluster.x-k8s.io", "openstackmachinetemplates.infrastructure.cluster.x-k8s.io", "openstackmachines.infrastructure.cluster.x-k8s.io",
	}
	AzureResources = []string{
		"azureclusters.infrastructure.cluster.x-k8s.io", "azuremachinetemplates.infrastructure.cluster.x-k8s.io", "azuremachines.infrastructure.cluster.x-k8s.io",
	}

	// Platform resource mapping
	PlatformResourceMap = map[string][]string{
		"AWS":       AWSResources,
		"AGENT":     AgentResources,
		"KUBEVIRT":  KubevirtResources,
		"OPENSTACK": OpenstackResources,
		"AZURE":     AzureResources,
	}
)

// GetDefaultResourcesForPlatform returns the default resource list based on the platform
func GetDefaultResourcesForPlatform(platform string) []string {
	// Get platform-specific resources, default to AWS if platform is unknown
	platformResources, exists := PlatformResourceMap[strings.ToUpper(platform)]
	if !exists {
		platformResources = AWSResources
	}

	// Combine base and platform-specific resources
	result := make([]string, len(BaseResources)+len(platformResources))
	copy(result, BaseResources)
	copy(result[len(BaseResources):], platformResources)

	return result
}