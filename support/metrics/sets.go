package metrics

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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

	SREConfigurationConfigMapName = "sre-metric-set"
	SREConfigurationConfigMapKey  = "config"
)

var (
	allMetricsSets = sets.NewString()

	sreMetricsSetConfig = MetricsSetConfig{}
	sreMetricsSetMutex  = sync.Mutex{}
)

type MetricsSetConfig struct {
	// Kube/OpenShift components
	Etcd                            []*prometheusoperatorv1.RelabelConfig `json:"etcd,omitempty"`
	KubeAPIServer                   []*prometheusoperatorv1.RelabelConfig `json:"kubeAPIServer,omitempty"`
	KubeControllerManager           []*prometheusoperatorv1.RelabelConfig `json:"kubeControllerManager,omitempty"`
	OpenShiftAPIServer              []*prometheusoperatorv1.RelabelConfig `json:"openshiftAPIServer,omitempty"`
	OpenShiftControllerManager      []*prometheusoperatorv1.RelabelConfig `json:"openshiftControllerManager,omitempty"`
	OpenShiftRouteControllerManager []*prometheusoperatorv1.RelabelConfig `json:"openshiftRouteControllerManager,omitempty"`
	CVO                             []*prometheusoperatorv1.RelabelConfig `json:"cvo,omitempty"`
	CCO                             []*prometheusoperatorv1.RelabelConfig `json:"cco,omitempty"`
	OLM                             []*prometheusoperatorv1.RelabelConfig `json:"olm,omitempty"`
	CatalogOperator                 []*prometheusoperatorv1.RelabelConfig `json:"catalogOperator,omitempty"`
	RegistryOperator                []*prometheusoperatorv1.RelabelConfig `json:"registryOperator,omitempty"`
	NodeTuningOperator              []*prometheusoperatorv1.RelabelConfig `json:"nodeTuningOperator,omitempty"`

	// HyperShift components
	ControlPlaneOperator        []*prometheusoperatorv1.RelabelConfig `json:"controlPlaneOperator,omitempty"`
	HostedClusterConfigOperator []*prometheusoperatorv1.RelabelConfig `json:"hostedClusterConfigOperator,omitempty"`
}

func (c *MetricsSetConfig) LoadFromString(value string) error {
	newConfig := &MetricsSetConfig{}
	if err := yaml.Unmarshal([]byte(value), newConfig); err != nil {
		return fmt.Errorf("failed to unmarshal metrics set configuration: %w", err)
	}
	*c = *newConfig
	return nil
}

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
	case MetricsSetSRE:
		return sreMetricsSetConfig.CVO
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.Etcd
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.KubeAPIServer
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.KubeControllerManager
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

func NTORelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "keep",
				Regex:        "nto_profile_calculated_total",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	case MetricsSetSRE:
		return sreMetricsSetConfig.NodeTuningOperator

	default:
		// All metrics
		return nil
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.OpenShiftAPIServer
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.OpenShiftControllerManager
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

func OpenShiftRouteControllerManagerRelabelConfigs(set MetricsSet) []*prometheusoperatorv1.RelabelConfig {
	switch set {
	case MetricsSetTelemetry:
		return []*prometheusoperatorv1.RelabelConfig{
			{
				Action:       "drop",
				Regex:        "(.*)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	case MetricsSetSRE:
		return sreMetricsSetConfig.OpenShiftRouteControllerManager
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.OLM
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
	case MetricsSetSRE:
		return sreMetricsSetConfig.CatalogOperator
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
	// For now, no filtering will occur for the HCCO
	return nil
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
				Regex:        "(.*)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			},
		}
	case MetricsSetSRE:
		return sreMetricsSetConfig.RegistryOperator
	}
	return nil
}

// SREMetricsSetConfigHash calculates a hash of the SRE metrics set configuration
// given a ConfigMap that contains it.
func SREMetricsSetConfigHash(cm *corev1.ConfigMap) string {
	value, ok := cm.Data[SREConfigurationConfigMapKey]
	if ok {
		return util.HashSimple(value)
	}
	return ""
}

// LoadSREMetricsSetConfigurationFromConfigMap parses the SRE metrics set configuration
// from the given ConfigMap and loads it into the singleton variable 'sreMetricsSetConfig'
// This can then be used by reconcile functions that get lists of RelabelConfigs for a
// particular component.
func LoadSREMetricsSetConfigurationFromConfigMap(cm *corev1.ConfigMap) error {
	sreMetricsSetMutex.Lock()
	defer sreMetricsSetMutex.Unlock()
	value, ok := cm.Data[SREConfigurationConfigMapKey]
	if !ok {
		return fmt.Errorf("configmap does not contain configuration key %s", SREConfigurationConfigMapKey)
	}
	return sreMetricsSetConfig.LoadFromString(value)
}

// SREMetricsSetConfigurationConfigMap returns a ConfigMap manifest for the SRE metrics set
// configuration, given a namespace. This configmap is expected to be created by the HyperShift administrator
// in the hypershift operator's namespace. It is then synced from there to every control plane namespace
// by the hypershift operator.
func SREMetricsSetConfigurationConfigMap(namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      SREConfigurationConfigMapName,
		},
	}
}
