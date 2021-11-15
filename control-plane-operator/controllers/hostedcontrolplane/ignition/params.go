package ignition

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type IgnitionParams struct {
	OwnerRef                 config.OwnerRef
	FIPSEnabled              bool
	SSHKey                   string
	APIServerInternalAddress string
}

func NewIgnitionParams(hcp *hyperv1.HostedControlPlane, images map[string]string, sshKey string) *IgnitionParams {
	params := &IgnitionParams{
		OwnerRef:    config.OwnerRefFrom(hcp),
		FIPSEnabled: hcp.Spec.FIPS,
		SSHKey:      sshKey,
	}

	if hcp.Spec.APIAdvertiseAddress != nil {
		params.APIServerInternalAddress = *hcp.Spec.APIAdvertiseAddress
	} else {
		params.APIServerInternalAddress = config.DefaultAdvertiseAddress
	}

	return params
}
