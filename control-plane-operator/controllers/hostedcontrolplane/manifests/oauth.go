package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	//TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"
)

func OAuthServerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift",
			Namespace: ns,
		},
	}
}

func OAuthServerPodDisruptionBudget(ns string) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift",
			Namespace: ns,
		},
	}
}

func OAuthServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift",
			Namespace: ns,
		},
	}
}

func OAuthServerService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift",
			Namespace: ns,
		},
	}
}

func OAuthServerCertWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-oauth-serving-cert",
			Namespace: ns,
		},
	}
}

func OAuthServerCert() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-serving-cert",
			Namespace: "openshift-config-managed",
		},
	}
}

func OAuthServerServiceSessionSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift-session",
			Namespace: ns,
		},
	}
}

func OAuthServerDefaultLoginTemplateSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift-default-login-template",
			Namespace: ns,
		},
	}
}

func OAuthServerDefaultProviderSelectionTemplateSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift-default-provider-selection-template",
			Namespace: ns,
		},
	}
}

func OAuthServerDefaultErrorTemplateSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift-default-error-template",
			Namespace: ns,
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

func OAuthServerChallengingClientManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-challenging-client",
			Namespace: ns,
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

func OAuthServerBrowserClientManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-browser-client",
			Namespace: ns,
		},
	}
}
