package ignition

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type IgnitionParams struct {
	OwnerRef                 config.OwnerRef
	FIPSEnabled              bool
	SSHKey                   string
	HAProxyImage             string
	APIServerExternalAddress string
	APIServerExternalPort    int32
	APIServerInternalAddress string
	APIServerInternalPort    int32
}

func NewIgnitionParams(hcp *hyperv1.HostedControlPlane, images map[string]string, apiServerAddress string, apiServerPort int32, sshKey string) *IgnitionParams {
	params := &IgnitionParams{
		OwnerRef:                 config.OwnerRefFrom(hcp),
		FIPSEnabled:              hcp.Spec.FIPS,
		SSHKey:                   sshKey,
		HAProxyImage:             images["haproxy-router"],
		APIServerExternalAddress: apiServerAddress,
		APIServerExternalPort:    apiServerPort,
	}

	if hcp.Spec.APIAdvertiseAddress != nil {
		params.APIServerInternalAddress = *hcp.Spec.APIAdvertiseAddress
	} else {
		params.APIServerInternalAddress = config.DefaultAdvertiseAddress
	}
	if hcp.Spec.APIPort != nil {
		params.APIServerInternalPort = *hcp.Spec.APIPort
	} else {
		params.APIServerInternalPort = config.DefaultAPIServerPort
	}
	return params
}
