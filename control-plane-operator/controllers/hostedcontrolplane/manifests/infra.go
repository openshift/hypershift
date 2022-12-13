package manifests

import (
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeAPIServerServiceName        = "kube-apiserver"
	KubeAPIServerPrivateServiceName = "kube-apiserver-private"
	kubeAPIServerExternalRouteName  = "kube-apiserver"
	kubeAPIServerInternalRouteName  = "kube-apiserver-internal"
	oauthServiceName                = "oauth-openshift"
	oauthExternalRouteName          = "oauth"
	oauthInternalRouteName          = "oauth-internal"
	konnectivityServerServiceName   = "konnectivity-server"
	openshiftAPIServerServiceName   = "openshift-apiserver"
	oauthAPIServerName              = "openshift-oauth-apiserver"
	packageServerServiceName        = "packageserver"
)

func KubeAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerPrivateService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerPrivateServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerExternalRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerExternalRouteName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerInternalRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerInternalRouteName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oauthServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthServerExternalRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      oauthExternalRouteName,
		},
	}
}

func OauthServerInternalRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      oauthInternalRouteName,
		},
	}
}

func KonnectivityServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      konnectivityServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KonnectivityServerRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      konnectivityServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OpenshiftAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openshiftAPIServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oauthAPIServerName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OLMPackageServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      packageServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}
