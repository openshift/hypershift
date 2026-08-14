package manifests

import (
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeAPIServerServiceName                = "kube-apiserver"
	KubeAPIServerPrivateServiceName         = "kube-apiserver-private"
	kubeAPIServerExternalPublicRouteName    = "kube-apiserver"
	kubeAPIServerExternalPrivateRouteName   = "kube-apiserver-private"
	kubeAPIServerInternalRouteName          = "kube-apiserver-internal"
	kubeAPIServerExternalPrivateServiceName = "kube-apiserver-private-external"
	oauthServiceName                        = "oauth-openshift"
	oauthExternalRoutePublicName            = "oauth"
	oauthExternalRoutePrivateName           = "oauth-private"
	oauthInternalRouteName                  = "oauth-internal"
	oauthExternalPrivateServiceName         = "oauth-private-external"
	konnectivityServerServiceName           = "konnectivity-server"
	openshiftAPIServerServiceName           = "openshift-apiserver"
	oauthAPIServerName                      = "openshift-oauth-apiserver"
	packageServerServiceName                = "packageserver"
)

func KubeAPIServerServiceAzureLB(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName + "lb",
			Namespace: hostedClusterNamespace,
		},
	}
}

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

func KubeAPIServerExternalPublicRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerExternalPublicRouteName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerExternalPrivateRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerExternalPrivateRouteName,
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

func KubeAPIServerExternalPrivateService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerExternalPrivateServiceName,
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

func OauthServerExternalPublicRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      oauthExternalRoutePublicName,
		},
	}
}

func OauthServerExternalPrivateRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      oauthExternalRoutePrivateName,
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

func OauthServerExternalPrivateService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oauthExternalPrivateServiceName,
			Namespace: hostedClusterNamespace,
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
