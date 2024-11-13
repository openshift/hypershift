package pki

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/util"
)

type PKIParams struct {
	// ServiceCIDR
	// Subnet for cluster services
	ServiceCIDR []string `json:"serviceCIDR"`

	// ClusterCIDR
	// Subnet for pods
	ClusterCIDR []string `json:"clusterCIDR"`

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
	// A fixed IP that pods on worker nodes will use to communicate with the API server - 172.20.0.1 for IPv4 and fd00::1 in IPv6 case
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
	svcCIDRs := make([]string, 0)
	clusterCIDRs := make([]string, 0)

	// Go over all service networks in order to include them in the PKI certificates
	for _, svcCIDR := range hcp.Spec.Networking.ServiceNetwork {
		svcCIDRs = append(svcCIDRs, svcCIDR.CIDR.String())
	}

	// Go over all cluster networks in order to include them in the PKI certificates
	for _, clusterCIDR := range hcp.Spec.Networking.ClusterNetwork {
		clusterCIDRs = append(clusterCIDRs, clusterCIDR.CIDR.String())
	}

	p := &PKIParams{
		ServiceCIDR:                  svcCIDRs,
		ClusterCIDR:                  clusterCIDRs,
		Namespace:                    hcp.Namespace,
		ExternalAPIAddress:           apiExternalAddress,
		InternalAPIAddress:           fmt.Sprintf("api.%s.hypershift.local", hcp.Name),
		ExternalKconnectivityAddress: konnectivityExternalAddress,
		ExternalOauthAddress:         oauthExternalAddress,
		IngressSubdomain:             globalconfig.IngressDomain(hcp),
		OwnerRef:                     config.OwnerRefFrom(hcp),
	}

	// If the first serviceCIDR is an IPv4 we need to set the config.DefaultAdvertiseIPv4Address
	// as fake IP in the node to access the haproxy exposed as kube-api-server-proxy
	// Even with that, we cannot set more than one AdvertiseAddress so both
	// are not supported at the same time.
	// Check this for more info: https://github.com/kubernetes/enhancements/issues/2438
	ipv4, err := util.IsIPv4(p.ServiceCIDR[0])
	if err != nil || ipv4 {
		p.NodeInternalAPIServerIP = util.AdvertiseAddressWithDefault(hcp, config.DefaultAdvertiseIPv4Address)
	} else {
		p.NodeInternalAPIServerIP = util.AdvertiseAddressWithDefault(hcp, config.DefaultAdvertiseIPv6Address)
	}

	return p
}
