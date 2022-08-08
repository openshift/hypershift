package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NamespaceOpenShiftAPIServer() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-apiserver",
		},
	}
}

func NamespaceOpenShiftControllerManager() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-controller-manager",
		},
	}
}

func NamespaceKubeAPIServer() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-kube-apiserver",
		},
	}
}

func NamespaceKubeControllerManager() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-kube-controller-manager",
		},
	}
}

func NamespaceKubeScheduler() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-kube-scheduler",
		},
	}
}

func NamespaceEtcd() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-etcd",
		},
	}
}

func NamespaceIngress() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-ingress",
		},
	}
}

func NamespaceAuthentication() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-authentication",
		},
	}
}

func NamespaceRouteControllerManager() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "openshift-route-controller-manager"},
	}
}
