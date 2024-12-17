package manifests

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func KubeAPIServerServiceMonitor() *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-kube-apiserver",
			Namespace: "openshift-kube-apiserver",
		},
	}
}
