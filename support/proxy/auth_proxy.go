package proxy

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

// AuthProxyOverride returns the auth-scoped proxy override for the external
// OIDC authentication components: spec.operatorConfiguration.authentication.proxy
// on the HostedControlPlane, or nil when it is unset.
//
// The override is the raw operatorv1.AuthenticationProxyConfig. Its NoProxy is
// a []string; consumers that need a comma-separated string join it themselves
// (the guest Console expects the slice shape and must never receive a joined
// value).
//
// The override is not merged with any other proxy source. When it is unset the
// external-oidc-webhook falls back to the management cluster proxy from the
// control plane operator process environment, so callers must not substitute
// the cluster-wide spec.configuration.proxy here.
func AuthProxyOverride(hcp *hyperv1.HostedControlPlane) *operatorv1.AuthenticationProxyConfig {
	if hcp == nil {
		return nil
	}

	if oc := hcp.Spec.OperatorConfiguration; oc != nil {
		if authProxy := oc.Authentication.Proxy; !isZeroAuthProxy(authProxy) {
			return &authProxy
		}
	}

	return nil
}

// isZeroAuthProxy reports whether the override carries no proxy configuration.
func isZeroAuthProxy(p operatorv1.AuthenticationProxyConfig) bool {
	return p.HTTPProxy == "" && p.HTTPSProxy == ""
}
