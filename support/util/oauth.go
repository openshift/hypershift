package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func HCPOAuthEnabled(hcp *hyperv1.HostedControlPlane) bool {
	return oauthEnabled(hcp.Spec.Configuration)
}

func ConfigOAuthEnabled(authentication *configv1.AuthenticationSpec) bool {
	if authentication != nil &&
		authentication.Type == configv1.AuthenticationTypeOIDC {
		return false
	}
	return true
}

func oauthEnabled(config *hyperv1.ClusterConfiguration) bool {
	if config != nil {
		return ConfigOAuthEnabled(config.Authentication)
	}
	return true
}
