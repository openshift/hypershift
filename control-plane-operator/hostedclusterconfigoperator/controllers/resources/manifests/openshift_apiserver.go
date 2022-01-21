package manifests

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

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

func OpenShiftAPIServerAPIService(group string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("v1.%s.openshift.io", group),
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

func OpenShiftAPIServerService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
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
