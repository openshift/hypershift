package ignition

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
)

type IgnitionConfigParams struct {
	OwnerRef               config.OwnerRef
	FIPSEnabled            bool
	SSHKey                 string
	HasImageMirrorPolicies bool
}

func NewIgnitionConfigParams(hcp *hyperv1.HostedControlPlane, sshKey string) *IgnitionConfigParams {
	params := &IgnitionConfigParams{
		OwnerRef:               config.OwnerRefFrom(hcp),
		FIPSEnabled:            hcp.Spec.FIPS,
		SSHKey:                 sshKey,
		HasImageMirrorPolicies: len(hcp.Spec.ImageContentSources) > 0 || len(hcp.Spec.ImageTagMirrorSet) > 0,
	}

	return params
}
