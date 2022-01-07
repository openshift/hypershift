package manifests

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

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

func OpenShiftOAuthAPIServerAPIService(group string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("v1.%s.openshift.io", group),
		},
	}
}

func OpenShiftOAuthAPIServerAPIServiceGroups() []string {
	return []string{
		"oauth",
		"user",
	}
}

func OpenShiftOAuthAPIServerService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: ns,
		},
	}
}
