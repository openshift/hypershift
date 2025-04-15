package util

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetHostedClusterManagedResources(platformsInstalled string) []client.Object {
	var managedResources []client.Object

	platformsInstalledList := strings.Split(platformsInstalled, ",")
	for _, platform := range platformsInstalledList {
		platform = strings.ToLower(strings.TrimSpace(platform))
		switch platform {
		case "aws":
			managedResources = append(managedResources, &capiaws.AWSCluster{})
			managedResources = append(managedResources, &hyperv1.AWSEndpointService{})
		case "azure":
			managedResources = append(managedResources, &capiazure.AzureCluster{})
		case "ibmcloud":
			managedResources = append(managedResources, &capiibmv1.IBMVPCCluster{})
		case "kubevirt":
			managedResources = append(managedResources, &capikubevirt.KubevirtCluster{})
		case "agent":
			managedResources = append(managedResources, &agentv1.AgentCluster{})
		case "openstack":
			managedResources = append(managedResources, &capiopenstackv1beta1.OpenStackCluster{})
		}
	}

	return managedResources
}

func GetNodePoolManagedResources(platformsInstalled string) []client.Object {
	var managedResources []client.Object

	platformsInstalledList := strings.Split(platformsInstalled, ",")
	for _, platform := range platformsInstalledList {
		platform = strings.ToLower(strings.TrimSpace(platform))
		switch platform {
		case "aws":
			managedResources = append(managedResources, &capiaws.AWSMachineTemplate{})
		case "azure":
			managedResources = append(managedResources, &capiazure.AzureMachineTemplate{})
		case "agent":
			managedResources = append(managedResources, &agentv1.AgentMachineTemplate{})
		case "openstack":
			managedResources = append(managedResources, &capiopenstackv1beta1.OpenStackMachineTemplate{})
		}
	}

	return managedResources
}
