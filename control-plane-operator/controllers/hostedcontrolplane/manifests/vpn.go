package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func VPNService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-server",
			Namespace: ns,
		},
	}
}

func VPNServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpn",
			Namespace: ns,
		},
	}
}

func VPNKubeAPIServerClientConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-kube-apiserver-client",
			Namespace: ns,
		},
	}
}

func VPNServerClientConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-ccd",
			Namespace: ns,
		},
	}
}

func VPNServerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-server",
			Namespace: ns,
		},
	}
}

func VPNServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-server",
			Namespace: ns,
		},
	}
}

func VPNClientConfig() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-client",
			Namespace: "kube-system",
		},
	}
}

func VPNWorkerClientConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openvpn-client-config",
			Namespace: ns,
		},
	}
}

func VPNClientDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openvpn-client",
			Namespace: "kube-system",
		},
	}
}

func VPNWorkerClientDeployment(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openvpn-client-deployment",
			Namespace: ns,
		},
	}
}
