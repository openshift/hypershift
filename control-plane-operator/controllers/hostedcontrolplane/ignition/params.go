package ignition

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
)

type IgnitionConfigParams struct {
	OwnerRef                    config.OwnerRef
	FIPSEnabled                 bool
	SSHKey                      string
	HasImageContentSourcePolicy bool
}

func NewIgnitionConfigParams(hcp *hyperv1.HostedControlPlane, sshKey string) *IgnitionConfigParams {
	params := &IgnitionConfigParams{
		OwnerRef:                    config.OwnerRefFrom(hcp),
		FIPSEnabled:                 hcp.Spec.FIPS,
		SSHKey:                      sshKey,
		HasImageContentSourcePolicy: len(hcp.Spec.ImageContentSources) > 0,
	}

	return params
}
