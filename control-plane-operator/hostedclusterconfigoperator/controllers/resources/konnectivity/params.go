package konnectivity

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	systemNodeCriticalPriorityClass = "system-node-critical"

	// AgentSecretHashAnnotation is set on the konnectivity-agent DaemonSet pod template.
	// Its value is a hash of the agent client certificate Secret data in the hosted cluster.
	// The proxy-agent reads its client certificate only at startup, so a change of this
	// value rolls the DaemonSet and makes the agents load the rotated certificate.
	AgentSecretHashAnnotation = "hypershift.openshift.io/konnectivity-agent-secret-hash"
)

type KonnectivityParams struct {
	Image           string
	ExternalAddress string
	ExternalPort    int32

	AdditionalAnnotations map[string]string

	// AgentSecretHash is the hash of the konnectivity-agent Secret data that was
	// successfully written to the hosted cluster. When empty, for example because
	// the Secret could not be read or written, the hash already present on the
	// DaemonSet pod template is kept so that the agents are not rolled.
	AgentSecretHash string
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
