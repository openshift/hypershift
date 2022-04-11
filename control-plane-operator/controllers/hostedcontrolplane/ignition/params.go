package ignition

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type IgnitionConfigParams struct {
	OwnerRef                    config.OwnerRef
	FIPSEnabled                 bool
	SSHKey                      string
	HAProxyImage                string
	CPOImage                    string
	APIServerExternalAddress    string
	APIServerExternalPort       int32
	APIServerInternalAddress    string
	APIServerInternalPort       int32
	HasImageContentSourcePolicy bool
}

func NewIgnitionConfigParams(hcp *hyperv1.HostedControlPlane, images map[string]string, apiServerAddress string, apiServerPort int32, sshKey string) *IgnitionConfigParams {
	params := &IgnitionConfigParams{
		OwnerRef:                    config.OwnerRefFrom(hcp),
		FIPSEnabled:                 hcp.Spec.FIPS,
		SSHKey:                      sshKey,
		HAProxyImage:                images["haproxy-router"],
		CPOImage:                    images[util.CPOImageName],
		APIServerExternalAddress:    apiServerAddress,
		APIServerExternalPort:       apiServerPort,
		HasImageContentSourcePolicy: len(hcp.Spec.ImageContentSources) > 0,
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
