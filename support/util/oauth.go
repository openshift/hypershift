package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func HCPOAuthEnabled(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Spec.Configuration == nil || ConfigOAuthEnabled(hcp.Spec.Configuration.Authentication)
}

func ConfigOAuthEnabled(authentication *configv1.AuthenticationSpec) bool {
	if authentication == nil {
		return true
	}

	switch authentication.Type {
	case configv1.AuthenticationTypeIntegratedOAuth, configv1.AuthenticationTypeNone, "":
		return true
	default:
		return false
	}
}
