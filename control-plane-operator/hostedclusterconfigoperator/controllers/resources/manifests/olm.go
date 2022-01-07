package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func CertifiedOperatorsCatalogSource() *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "certified-operators",
			Namespace: "openshift-marketplace",
		},
	}
}

func CommunityOperatorsCatalogSource() *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "community-operators",
			Namespace: "openshift-marketplace",
		},
	}
}

func RedHatMarketplaceCatalogSource() *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-marketplace",
			Namespace: "openshift-marketplace",
		},
	}
}

func RedHatOperatorsCatalogSource() *operatorsv1alpha1.CatalogSource {
	return &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redhat-operators",
			Namespace: "openshift-marketplace",
		},
	}
}

func OLMPackageServerAPIService() *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: "v1.packages.operators.coreos.com",
		},
	}
}

func OLMPackageServerService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver",
			Namespace: "default",
		},
	}
}

func OLMPackageServerControlPlaneService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver",
			Namespace: ns,
		},
	}
}

func OLMPackageServerEndpoints() *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "packageserver",
			Namespace: "default",
		},
	}
}

func OLMAlertRules() *prometheusoperatorv1.PrometheusRule {
	return &prometheusoperatorv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "olm-alert-rules",
			Namespace: "openshift-operator-lifecycle-manager",
		},
	}
}
