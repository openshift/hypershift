package manifests

import (
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	kubeAPIServerServiceName      = "kube-apiserver"
	OauthServiceName              = "oauth-openshift"
	oauthRouteName                = "oauth"
	vpnServiceName                = "openvpn-server"
	openshiftAPIServerServiceName = "openshift-apiserver"
	oauthAPIServerName            = "openshift-oauth-apiserver"
)

func KubeAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeAPIServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OauthServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func OauthServerRoute(hostedClusterNamespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hostedClusterNamespace,
			Name:      oauthRouteName,
		},
	}
}

func VPNServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vpnServiceName,
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
