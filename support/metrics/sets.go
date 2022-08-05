package metrics

import (
	"fmt"
	"os"
	"strings"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

type MetricsSet string

const (
	MetricsSetTelemetry MetricsSet = "Telemetry"
	MetricsSetSRE       MetricsSet = "SRE"
	MetricsSetAll       MetricsSet = "All"
)

const (
	metricsSetEnvVar  = "METRICS_SET"
	DefaultMetricsSet = MetricsSetTelemetry
)

var (
	allMetricsSets = sets.NewString()
)

func init() {
	allMetricsSets.Insert(string(MetricsSetTelemetry))
	allMetricsSets.Insert(string(MetricsSetSRE))
	allMetricsSets.Insert(string(MetricsSetAll))
}

func MetricsSetFromString(str string) (MetricsSet, error) {
	if str == "" {
		return DefaultMetricsSet, nil
	}
	for _, value := range allMetricsSets.List() {
		if strings.EqualFold(string(value), str) {
			return MetricsSet(value), nil
		}
	}
	return "", fmt.Errorf("invalid metrics set: %s", str)
}

func MetricsSetFromEnv() (MetricsSet, error) {
	return MetricsSetFromString(os.Getenv(metricsSetEnvVar))
}

func MetricsSetToEnv(set MetricsSet) corev1.EnvVar {
	return corev1.EnvVar{
		Name:  metricsSetEnvVar,
		Value: string(set),
	}
}

func (s *MetricsSet) String() string {
	return string(*s)
}

func (s *MetricsSet) Set(value string) error {
	var err error
	*s, err = MetricsSetFromString(value)
	return err
}

func (s *MetricsSet) Type() string {
	return "metricsSet"
}

func CVORelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(cluster_version|cluster_version_available_updates|cluster_operator_up|cluster_operator_conditions|cluster_version_payload|cluster_installer)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}

	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
}

func EtcdRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(etcd_disk_wal_fsync_duration_seconds_bucket|etcd_mvcc_db_total_size_in_bytes|etcd_network_peer_round_trip_time_seconds_bucket|etcd_mvcc_db_total_size_in_use_in_bytes|etcd_disk_backend_commit_duration_seconds_bucket|etcd_server_leader_changes_seen_total)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}

	default:
		// All metrics
		return nil
	}
}

func KASRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(apiserver_storage_objects|apiserver_request_total|apiserver_current_inflight_requests)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_admission_controller_admission_latencies_seconds_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_admission_step_admission_latencies_seconds_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "scheduler_(e2e_scheduling_latency_microseconds|scheduling_algorithm_predicate_evaluation|scheduling_algorithm_priority_evaluation|scheduling_algorithm_preemption_evaluation|scheduling_algorithm_latency_microseconds|binding_latency_microseconds|scheduling_latency_seconds)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_(request_count|request_latencies|request_latencies_summary|dropped_requests|storage_data_key_generation_latencies_microseconds|storage_transformation_failures_total|storage_transformation_latencies_microseconds|proxy_tunnel_sync_latency_secs)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "docker_(operations|operations_latency_microseconds|operations_errors|operations_timeout)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "reflector_(items_per_list|items_per_watch|list_duration_seconds|lists_total|short_watches_total|watch_duration_seconds|watches_total)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "etcd_(helper_cache_hit_count|helper_cache_miss_count|helper_cache_entry_count|request_cache_get_latencies_summary|request_cache_add_latencies_summary|request_latencies_summary)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "transformation_(transformation_latencies_microseconds|failures_total)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "network_plugin_operations_latency_microseconds|sync_proxy_rules_latency_microseconds|rest_client_request_latency_seconds",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_request_duration_seconds_bucket;(0.15|0.25|0.3|0.35|0.4|0.45|0.6|0.7|0.8|0.9|1.25|1.5|1.75|2.5|3|3.5|4.5|6|7|8|9|15|25|30|50)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			},
		}
	}
}

func KCMRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "pv_collector_total_pv_count",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|request|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "rest_client_request_latency_seconds_(bucket|count|sum)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "root_ca_cert_publisher_sync_duration_seconds_(bucket|count|sum)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
}

func OpenShiftAPIServerRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(apiserver_storage_objects|apiserver_request_total|apiserver_current_inflight_requests)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_admission_controller_admission_latencies_seconds_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_admission_step_admission_latencies_seconds_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
			{
				Action:       "drop",
				Regex:        "apiserver_request_duration_seconds_bucket;(0.15|0.25|0.3|0.35|0.4|0.45|0.6|0.7|0.8|0.9|1.25|1.5|1.75|2.5|3|3.5|4.5|6|7|8|9|15|25|30|50)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
			},
		}
	}
}

func OpenShiftControllerManagerRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "openshift_build_status_phase_total",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|request|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
}

func OLMRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(csv_succeeded|csv_abnormal)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
}

func CatalogOperatorRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(subscription_sync_total|olm_resolution_duration_seconds)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
}

func HostedClusterConfigOperatorRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "(cluster_infrastructure_provider|cluster_feature_set)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	default:
		return nil
	}
}

func ControlPlaneOperatorRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	// For now, no filtering will occur for the CPO
	return nil
}

func RegistryOperatorRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	}
	return nil
}
