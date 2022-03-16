package kas

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, apiServerPort int, ownerRef config.OwnerRef, clusterID string) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = kasLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromInt(apiServerPort)
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "kube-apiserver",
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
					Regex:        "scheduler_(e2e_scheduling_latency_microseconds|scheduling_algorithm_predicate_evaluation|scheduling_algorithm_priority_evaluation|scheduling_algorithm_preemption_evaluation|scheduling_algorithm_latency_microseconds|binding_latency_microseconds|scheduling_latency_seconds)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "apiserver_(request_count|request_latencies|request_latencies_summary|dropped_requests|storage_data_key_generation_latencies_microseconds|storage_transformation_failures_total|storage_transformation_latencies_microseconds|proxy_tunnel_sync_latency_secs)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "docker_(operations|operations_latency_microseconds|operations_errors|operations_timeout)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "reflector_(items_per_list|items_per_watch|list_duration_seconds|lists_total|short_watches_total|watch_duration_seconds|watches_total)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "etcd_(helper_cache_hit_count|helper_cache_miss_count|helper_cache_entry_count|request_cache_get_latencies_summary|request_cache_add_latencies_summary|request_latencies_summary)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "transformation_(transformation_latencies_microseconds|failures_total)",
					SourceLabels: []string{"__name__"},
				},
				{
					Action:       "drop",
					Regex:        "network_plugin_operations_latency_microseconds|sync_proxy_rules_latency_microseconds|rest_client_request_latency_seconds",
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

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}
