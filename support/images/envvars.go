package images

import "os"

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
	SharedIngressHAProxyEnvVar        = "IMAGE_SHARED_INGRESS_HAPROXY"
)

const (
	// DefaultSharedIngressHAProxyImage is the default image for the shared ingress HAProxy
	DefaultSharedIngressHAProxyImage = "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:1af59b7a29432314bde54e8977fa45fa92dc48885efbf0df601418ec0912f472"
)

// GetSharedIngressHAProxyImage returns the shared ingress HAProxy image.
// It checks the IMAGE_SHARED_INGRESS_HAPROXY environment variable first,
// and falls back to the default hardcoded image if not set.
func GetSharedIngressHAProxyImage() string {
	if envImage := os.Getenv(SharedIngressHAProxyEnvVar); len(envImage) > 0 {
		return envImage
	}
	return DefaultSharedIngressHAProxyImage
}

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
