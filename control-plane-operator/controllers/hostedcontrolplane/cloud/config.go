package cloud

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"
)

func ProviderConfigKey(provider string) string {
	switch provider {
	case aws.Provider:
		return aws.ProviderConfigKey
	case azure.Provider:
		return azure.CloudConfigKey
	case openstack.Provider:
		return openstack.CloudConfigKey
	default:
		return ""
	}
}
