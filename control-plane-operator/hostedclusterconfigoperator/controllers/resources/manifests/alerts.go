package manifests

import (
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ApiUsageRule() *prometheusoperatorv1.PrometheusRule {
	return &prometheusoperatorv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-usage",
			Namespace: "openshift-kube-apiserver",
		},
	}
}
