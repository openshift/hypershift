package oapi

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = openshiftAPIServerLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("https")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "openshift-apiserver",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "tls.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
						},
						Key: "tls.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "ca.crt",
						},
					},
				},
			},
			MetricRelabelConfigs: []*prometheusoperatorv1.RelabelConfig{
				{
					Action:       "drop",
					Regex:        "etcd_(debugging|disk|server).*",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "apiserver_admission_controller_admission_latencies_seconds_.*",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "apiserver_admission_step_admission_latencies_seconds_.*",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "apiserver_request_duration_seconds_bucket;(0.15|0.25|0.3|0.35|0.4|0.45|0.6|0.7|0.8|0.9|1.25|1.5|1.75|2.5|3|3.5|4.5|6|7|8|9|15|25|30|50)",
					SourceLabels: []string{"__name__", "le"},
				},
			},
		},
	}

	return nil
}
