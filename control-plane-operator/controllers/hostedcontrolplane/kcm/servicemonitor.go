package kcm

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = kcmLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("client")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "kube-controller-manager",
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
					Regex:        "etcd_(debugging|disk|request|server).*",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "rest_client_request_latency_seconds_(bucket|count|sum)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "root_ca_cert_publisher_sync_duration_seconds_(bucket|count|sum)",
					SourceLabels: []string{"__name__"},
				},
			},
		},
	}

	return nil
}
