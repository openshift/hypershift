package konnectivity

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
)

type KonnectivityServiceParams struct {
	OwnerRef config.OwnerRef
}

func NewKonnectivityServiceParams(hcp *hyperv1.HostedControlPlane) *KonnectivityServiceParams {
	return &KonnectivityServiceParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
}
