package integration

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/reconcilerpolicy"
)

// Params holds the narrow facts the projections consume. It never retains the HCP.
type Params struct {
	// Namespace is the HCP namespace that holds the control-plane source objects.
	Namespace string
	// OAuthEnabled selects the OAuth projections; it is false for external OIDC.
	OAuthEnabled bool
	// OAuthAddress and OAuthPort are the external OAuth server endpoint used by OAuth clients.
	OAuthAddress string
	OAuthPort    int32
}

// ParamsFor derives Params from the HCP namespace and OAuth enablement and the external OAuth endpoint.
func ParamsFor(hcp *hyperv1.HostedControlPlane, oauthAddress string, oauthPort int32) Params {
	return Params{
		Namespace:    hcp.Namespace,
		OAuthEnabled: reconcilerpolicy.HCPOAuthEnabled(hcp),
		OAuthAddress: oauthAddress,
		OAuthPort:    oauthPort,
	}
}
