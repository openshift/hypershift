package pki

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type PKIParams struct {
	// Network - used to obtain the ServiceCIDR of the cluster
	Network configv1.Network `json:"network"`

	// ExternalAPIAddress
	// An externally accessible DNS name or IP for the API server. Currently obtained from the load balancer DNS name.
	ExternalAPIAddress string `json:"externalAPIAddress"`

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

	// VPN Server
	// An externally accessible DNS name or IP for the VPN Server. Currently obtained from VPN load balancer DNS name.
	ExternalOpenVPNAddress string `json:"externalOpenVPNAddress"`

	// Namespace used to generate internal DNS names for services.
	Namespace string `json:"namespace"`

	// Owner reference for resources
	OwnerReference *metav1.OwnerReference `json:"ownerReference"`
}

func NewPKIParams(hcp *hyperv1.HostedControlPlane,
	apiExternalAddress,
	oauthExternalAddress,
	vpnExternalAddress,
	konnectivityExternalAddress string) *PKIParams {
	p := &PKIParams{
		Namespace:                    hcp.Namespace,
		Network:                      config.Network(hcp),
		ExternalAPIAddress:           apiExternalAddress,
		ExternalKconnectivityAddress: konnectivityExternalAddress,
		NodeInternalAPIServerIP:      config.DefaultAdvertiseAddress,
		ExternalOauthAddress:         oauthExternalAddress,
		IngressSubdomain:             fmt.Sprintf("apps.%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain),
		ExternalOpenVPNAddress:       vpnExternalAddress,
		OwnerReference:               config.ControllerOwnerRef(hcp),
	}
	return p
}
