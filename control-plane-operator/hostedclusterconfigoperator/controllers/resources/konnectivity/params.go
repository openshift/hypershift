package konnectivity

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	systemNodeCriticalPriorityClass = "system-node-critical"
)

type KonnectivityParams struct {
	Image           string
	ExternalAddress string
	ExternalPort    int32

	AdditionalAnnotations map[string]string
}

func NewKonnectivityParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32) *KonnectivityParams {
	p := &KonnectivityParams{
		Image:                 images["konnectivity-agent"],
		ExternalAddress:       externalAddress,
		ExternalPort:          externalPort,
		AdditionalAnnotations: map[string]string{},
	}

	// check apiserver-network-proxy image in ocp payload and use it
	if _, ok := images["apiserver-network-proxy"]; ok {
		p.Image = images["apiserver-network-proxy"]
	}
	if _, ok := hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]; ok {
		p.Image = hcp.Annotations[hyperv1.KonnectivityAgentImageAnnotation]
	}

	p.AdditionalAnnotations[hyperv1.ReleaseImageAnnotation] = hcp.Spec.ReleaseImage
	if _, ok := hcp.Annotations[hyperv1.RestartDateAnnotation]; ok {
		p.AdditionalAnnotations[hyperv1.RestartDateAnnotation] = hcp.Annotations[hyperv1.RestartDateAnnotation]
	}

	return p
}
