package globalconfig

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AuthenticationConfiguration() *configv1.Authentication {
	return &configv1.Authentication{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileAuthenticationConfiguration(authentication *configv1.Authentication, config *hyperv1.ClusterConfiguration, issuerURL string) error {
	if config != nil && config.Authentication != nil {
		authentication.Spec = *config.Authentication
	} else {
		// When configuration or authentication is removed, explicitly reset the spec
		// to default state (IntegratedOAuth). This ensures the authentication-operator
		// properly clears OIDC configuration and falls back to OAuth.
		authentication.Spec = configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeIntegratedOAuth,
		}
	}
	authentication.Spec.ServiceAccountIssuer = issuerURL
	return nil
}
