package pki

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/support/config"
)

type PKIParams struct {
	// ServiceCIDR
	// Subnet for cluster services
	ServiceCIDR string `json:"serviceCIDR"`

	// PodCIDR
	// Subnet for pods
	PodCIDR string `json:"podCIDR"`

	// ExternalAPIAddress
	// An externally accessible DNS name or IP for the API server. Currently obtained from the load balancer DNS name.
	ExternalAPIAddress string `json:"externalAPIAddress"`

	// InternalAPIAddress
	// An internally accessible DNS name or IP for the API server.
	InternalAPIAddress string `json:"internalAPIAddress"`

	// ExternalKconnectivityAddress
	// An externally accessible DNS name or IP for the Konnectivity proxy. Currently obtained from the load balancer DNS name.
	ExternalKconnectivityAddress string `json:"externalKconnectivityAddress"`

	// NodeInternalAPIServerIP
	// A fixed IP that pods on worker nodes will use to communicate with the API server - 172.20.0.1
	NodeInternalAPIServerIP string `json:"nodeInternalAPIServerIP"`

	// ExternalOauthAddress
	// An externally accessible DNS name or IP for the Oauth server. Currently obtained from Oauth load balancer DNS name.
	ExternalOauthAddress string `json:"externalOauthAddress"`

	// IngressSubdomain
	// Subdomain for cluster ingress. Used to generate the wildcard certificate for ingress.
	IngressSubdomain string `json:"ingressSubdomain"`

	// Namespace used to generate internal DNS names for services.
	Namespace string `json:"namespace"`

	// Owner reference for resources
	OwnerRef config.OwnerRef `json:"ownerRef"`
}

func NewPKIParams(hcp *hyperv1.HostedControlPlane,
	apiExternalAddress,
	oauthExternalAddress,
	konnectivityExternalAddress string) *PKIParams {
	p := &PKIParams{
		ServiceCIDR:                  hcp.Spec.ServiceCIDR,
		PodCIDR:                      hcp.Spec.PodCIDR,
		Namespace:                    hcp.Namespace,
		ExternalAPIAddress:           apiExternalAddress,
		InternalAPIAddress:           fmt.Sprintf("api.%s.hypershift.local", hcp.Name),
		ExternalKconnectivityAddress: konnectivityExternalAddress,
		ExternalOauthAddress:         oauthExternalAddress,
		IngressSubdomain:             config.IngressSubdomain(hcp),
		OwnerRef:                     config.OwnerRefFrom(hcp),
	}
	if hcp.Spec.APIAdvertiseAddress != nil {
		p.NodeInternalAPIServerIP = *hcp.Spec.APIAdvertiseAddress
	} else {
		p.NodeInternalAPIServerIP = config.DefaultAdvertiseAddress
	}
	return p
}
