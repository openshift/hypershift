package configuration

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
)

type ProxyParams struct {
	Namespace string
	TrustedCA string
}

func ProxyParamsFor(namespace string, configuration *hyperv1.ClusterConfiguration) ProxyParams {
	params := ProxyParams{Namespace: namespace}
	if configuration != nil && configuration.Proxy != nil {
		params.TrustedCA = configuration.Proxy.TrustedCA.Name
	}
	return params
}

type ObservedConfigParams struct {
	Namespace string
	OwnerRef  config.OwnerRef
}
