package monitoring

import (
	"github.com/openshift/hypershift/support/metrics"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func ReconcileKubeAPIServerServiceMonitor(serviceMonitor *prometheusoperatorv1.ServiceMonitor) error {
	serviceMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{"default"},
	}
	serviceMonitor.Spec.Selector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"component": "apiserver",
		},
	}
	https := prometheusoperatorv1.Scheme("https")
	serviceMonitor.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			BearerTokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
			Scheme:          &https,
			Port:            "https",
			Path:            "/metrics",
			HTTPConfigWithProxyAndTLSFiles: prometheusoperatorv1.HTTPConfigWithProxyAndTLSFiles{
				HTTPConfigWithTLSFiles: prometheusoperatorv1.HTTPConfigWithTLSFiles{
					TLSConfig: &prometheusoperatorv1.TLSConfig{
						SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
							ServerName: ptr.To("kubernetes.default.svc"),
						},
						TLSFilesConfig: prometheusoperatorv1.TLSFilesConfig{
							CAFile: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.KASRelabelConfigs(metrics.MetricsSetAll),
		},
	}
	return nil
}
