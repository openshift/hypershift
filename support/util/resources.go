package util

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	"k8s.io/apimachinery/pkg/util/sets"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

var (
	BaseResources = []client.Object{
		&capiv1.Cluster{},
	}

	AWSResources = []client.Object{
		&capiaws.AWSCluster{},
		&hyperv1.AWSEndpointService{},
	}

	AzureResources = []client.Object{
		&capiazure.AzureCluster{},
		&capiazure.AzureClusterIdentity{},
	}

	ManagedAzure = []client.Object{
		&secretsstorev1.SecretProviderClass{},
	}

	IBMCloudResources = []client.Object{
		&capiibmv1.IBMVPCCluster{},
	}

	KubevirtResources = []client.Object{
		&capikubevirt.KubevirtCluster{},
	}

	AgentResources = []client.Object{
		&agentv1.AgentCluster{},
	}

	OpenStackResources = []client.Object{
		&capiopenstackv1beta1.OpenStackCluster{},
	}

	AWSNodePoolResources = []client.Object{
		&capiaws.AWSMachineTemplate{},
	}

	AzureNodePoolResources = []client.Object{
		&capiazure.AzureMachineTemplate{},
	}

	AgentNodePoolResources = []client.Object{
		&agentv1.AgentMachineTemplate{},
	}

	OpenStackNodePoolResources = []client.Object{
		&capiopenstackv1beta1.OpenStackMachineTemplate{},
	}
)

func GetHostedClusterManagedResources(platformsInstalled string) []client.Object {
	var managedResources []client.Object

	platformsInstalledList := sets.New[string]()
	for _, p := range strings.Split(platformsInstalled, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			platformsInstalledList.Insert(p)
		}
	}

	if platformsInstalledList.Len() == 0 {
		return managedResources
	}

	if platformsInstalledList.Len() == 1 && strings.EqualFold(platformsInstalledList.UnsortedList()[0], string(hyperv1.NonePlatform)) {
		return managedResources
	}

	// All platforms have the same base resources
	managedResources = append(managedResources, BaseResources...)

	for _, platform := range platformsInstalledList.UnsortedList() {
		switch {
		case strings.EqualFold(platform, string(hyperv1.AWSPlatform)):
			managedResources = append(managedResources, AWSResources...)
		case strings.EqualFold(platform, string(hyperv1.AzurePlatform)):
			managedResources = append(managedResources, AzureResources...)
		case strings.EqualFold(platform, string(hyperv1.IBMCloudPlatform)):
			managedResources = append(managedResources, IBMCloudResources...)
		case strings.EqualFold(platform, string(hyperv1.KubevirtPlatform)):
			managedResources = append(managedResources, KubevirtResources...)
		case strings.EqualFold(platform, string(hyperv1.AgentPlatform)):
			managedResources = append(managedResources, AgentResources...)
		case strings.EqualFold(platform, string(hyperv1.OpenStackPlatform)):
			managedResources = append(managedResources, OpenStackResources...)
		}
	}

	return managedResources
}

func GetNodePoolManagedResources(platformsInstalled string) []client.Object {
	var managedResources []client.Object

	platformsInstalledList := strings.Split(platformsInstalled, ",")
	for _, platform := range platformsInstalledList {
		platform = strings.ToLower(strings.TrimSpace(platform))
		switch {
		case strings.EqualFold(platform, string(hyperv1.AWSPlatform)):
			managedResources = append(managedResources, AWSNodePoolResources...)
		case strings.EqualFold(platform, string(hyperv1.AzurePlatform)):
			managedResources = append(managedResources, AzureNodePoolResources...)
		case strings.EqualFold(platform, string(hyperv1.AgentPlatform)):
			managedResources = append(managedResources, AgentNodePoolResources...)
		case strings.EqualFold(platform, string(hyperv1.OpenStackPlatform)):
			managedResources = append(managedResources, OpenStackNodePoolResources...)
		}
	}

	return managedResources
}
