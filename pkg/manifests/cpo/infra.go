package cpo

import (
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func KubeAPIServerExternalPublicRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerExternalPrivateRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver-private",
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerExternalPrivateService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver-private-external",
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-openshift",
			Namespace: hostedClusterNamespace,
			Labels: map[string]string{
				"app": "oauth-openshift",
			},
		},
	}
}

func OauthServerExternalPublicRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      "oauth",
		},
	}
}

func OauthServerExternalPrivateRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      "oauth-private",
		},
	}
}

func OauthServerExternalPrivateService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-private-external",
			Namespace: hostedClusterNamespace,
		},
	}
}

func RouterPublicService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: ns,
		},
	}
}
