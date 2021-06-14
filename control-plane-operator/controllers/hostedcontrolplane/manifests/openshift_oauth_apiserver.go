package manifests

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func OpenShiftOAuthAPIServerAuditConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver-audit",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerServiceServingCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver-serving-ca",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerClusterEndpoints() *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: "default",
		},
	}
}

func OpenShiftOAuthAPIServerClusterService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: "default",
		},
	}
}

func OpenShiftOAuthAPIServerWorkerEndpoints(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-oauth-apiserver-endpoints",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerWorkerService(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-oauth-apiserver-service",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerAPIService(group string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("v1.%s.openshift.io", group),
		},
	}
}

func OpenShiftOAuthAPIServerWorkerAPIService(group, ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("user-manifest-openshift-oauth-apiserver-apiservice-%s", group),
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerAPIServiceGroups() []string {
	return []string{
		"oauth",
		"user",
	}
}
