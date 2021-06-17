package manifests

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func OpenShiftAPIServerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerAuditConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver-audit",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerClusterEndpoints() *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: "default",
		},
	}
}

func OpenShiftAPIServerClusterService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: "default",
		},
	}
}

func OpenShiftAPIServerWorkerEndpoints(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-apiserver-endpoints",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerWorkerService(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-apiserver-service",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerAPIService(group string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("v1.%s.openshift.io", group),
		},
	}
}

func OpenShiftAPIServerWorkerAPIService(group, ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("user-manifest-openshift-apiserver-apiservice-%s", group),
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerAPIServiceGroups() []string {
	return []string{
		"apps",
		"authorization",
		"build",
		"image",
		"quota",
		"route",
		"security",
		"template",
		"project",
	}
}
