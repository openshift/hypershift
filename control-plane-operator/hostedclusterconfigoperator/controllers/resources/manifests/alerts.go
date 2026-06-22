package manifests

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func ApiUsageRule() *prometheusoperatorv1.PrometheusRule {
	return &prometheusoperatorv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-usage",
			Namespace: "openshift-kube-apiserver",
		},
	}
}

func PodSecurityViolationRule() *prometheusoperatorv1.PrometheusRule {
	return &prometheusoperatorv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podsecurity",
			Namespace: "openshift-kube-apiserver",
		},
	}
}
