package images

// Image environment variable constants
const (
	CAPIEnvVar                  = "IMAGE_CLUSTER_API"
	AgentCAPIProviderEnvVar     = "IMAGE_AGENT_CAPI_PROVIDER"
	AWSEncryptionProviderEnvVar = "IMAGE_AWS_ENCRYPTION_PROVIDER"
	AWSCAPIProviderEnvVar       = "IMAGE_AWS_CAPI_PROVIDER"
	AzureCAPIProviderEnvVar     = "IMAGE_AZURE_CAPI_PROVIDER"
	KubevirtCAPIProviderEnvVar  = "IMAGE_KUBEVIRT_CAPI_PROVIDER"
	KonnectivityEnvVar          = "IMAGE_KONNECTIVITY"
)

// TagMapping returns a mapping between tags in an image-refs ImageStream
// and the corresponding environment variable expected by the HyperShift operator
func TagMapping() map[string]string {
	return map[string]string{
		"apiserver-network-proxy":       KonnectivityEnvVar,
		"aws-encryption-provider":       AWSEncryptionProviderEnvVar,
		"cluster-api":                   CAPIEnvVar,
		"cluster-api-provider-agent":    AgentCAPIProviderEnvVar,
		"cluster-api-provider-aws":      AWSCAPIProviderEnvVar,
		"cluster-api-provider-azure":    AzureCAPIProviderEnvVar,
		"cluster-api-provider-kubevirt": KubevirtCAPIProviderEnvVar,
	}
}
