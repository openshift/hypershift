package manifests

import (
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RouterServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: ns,
		},
	}
}

func RouterRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: ns,
		},
	}
}

func RouterRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: ns,
		},
	}
}

func PrivateRouterService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: ns,
		},
	}
}

func RouterTemplateConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-template",
			Namespace: ns,
		},
	}
}

func ServiceProviderDefaultIngressServingCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-provider-default-ingress-serving-cert",
			Namespace: ns,
		},
	}
}

func IngressObservedDefaultIngressCertCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "observed-default-ingress-cert",
			Namespace: ns,
		},
	}
}

func MetricsForwarderRoute(ns string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-forwarder",
			Namespace: ns,
		},
	}
}

func MetricsProxyRoute(ns string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-proxy",
			Namespace: ns,
		},
	}
}
