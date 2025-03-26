package manifests

import (
	"github.com/openshift/api/annotations"
	oauthv1 "github.com/openshift/api/oauth/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func OAuthCABundle() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-serving-cert",
			Namespace: "openshift-config-managed",
			Annotations: map[string]string{
				annotations.OpenShiftComponent: "apiserver-auth",
			},
		},
	}
}

func OAuthServerChallengingClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-challenging-client",
		},
	}
}

func OAuthServerBrowserClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-browser-client",
		},
	}
}

func OAuthServerCLIClient() *oauthv1.OAuthClient {
	return &oauthv1.OAuthClient{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-cli-client",
		},
	}
}

func OAuthServingCertRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:oauth-servercert-trust",
			Namespace: "openshift-config-managed",
		},
	}
}

func OAuthServingCertRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:oauth-servercert-trust",
			Namespace: "openshift-config-managed",
		},
	}
}
