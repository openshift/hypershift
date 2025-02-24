package images

// Image environment variable constants
const (
	CAPIEnvVar                        = "IMAGE_CLUSTER_API"
	AgentCAPIProviderEnvVar           = "IMAGE_AGENT_CAPI_PROVIDER"
	AWSCAPIProviderEnvVar             = "IMAGE_AWS_CAPI_PROVIDER"
	AzureCAPIProviderEnvVar           = "IMAGE_AZURE_CAPI_PROVIDER"
	KubevirtCAPIProviderEnvVar        = "IMAGE_KUBEVIRT_CAPI_PROVIDER"
	PowerVSCAPIProviderEnvVar         = "IMAGE_POWERVS_CAPI_PROVIDER"
	KonnectivityEnvVar                = "IMAGE_KONNECTIVITY"
	OpenStackCAPIProviderEnvVar       = "IMAGE_OPENSTACK_CAPI_PROVIDER"
	OpenStackResourceControllerEnvVar = "IMAGE_OPENSTACK_RESOURCE_CONTROLLER"
)

// TagMapping returns a mapping between tags in an image-refs ImageStream
// and the corresponding environment variable expected by the HyperShift operator
func TagMapping() map[string]string {
	return map[string]string{
		"apiserver-network-proxy":        KonnectivityEnvVar,
		"cluster-api":                    CAPIEnvVar,
		"cluster-api-provider-agent":     AgentCAPIProviderEnvVar,
		"cluster-api-provider-aws":       AWSCAPIProviderEnvVar,
		"cluster-api-provider-azure":     AzureCAPIProviderEnvVar,
		"cluster-api-provider-kubevirt":  KubevirtCAPIProviderEnvVar,
		"cluster-api-provider-powervs":   PowerVSCAPIProviderEnvVar,
		"cluster-api-provider-openstack": OpenStackCAPIProviderEnvVar,
		"openstack-resource-controller":  OpenStackResourceControllerEnvVar,
	}
}
