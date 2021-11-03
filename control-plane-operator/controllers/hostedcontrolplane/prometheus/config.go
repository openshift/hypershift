package prometheus

import (
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/prometheus/common/model"
	promcfg "github.com/prometheus/prometheus/config"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const prometheusConfigFileName = "prometheus.yml"

func ReconcileConfiguration(cm *corev1.ConfigMap, ownerRef config.OwnerRef, apiServerPort *int32) error {
	cfg, err := buildPrometheusConfig(cm.Namespace, apiServerPort)
	if err != nil {
		return err
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[prometheusConfigFileName] = string(cfg.String())
	ownerRef.ApplyTo(cm)
	return nil
}

func buildPrometheusConfig(ns string, apiServerPort *int32) (*promcfg.Config, error) {
	guestRemoteWriteCfg, err := guestClusterPrometheusWriteConfig(apiServerPort)
	if err != nil {
		return nil, err
	}
	return &promcfg.Config{
		GlobalConfig: promcfg.GlobalConfig{
			ScrapeInterval: model.Duration(15 * time.Second),
		},
		ScrapeConfigs: []*promcfg.ScrapeConfig{
			kubeAPIServerScrapeConfig(ns),
		},
		RemoteWriteConfigs: []*promcfg.RemoteWriteConfig{
			guestRemoteWriteCfg,
		},
	}, nil
}

func kubeAPIServerScrapeConfig(ns string) *promcfg.ScrapeConfig {
	dropMetrics := []string{
		"etcd_(debugging|disk|server).*",
		"apiserver_admission_controller_admission_latencies_seconds_.*",
		"apiserver_admission_step_admission_latencies_seconds_.*",
		"scheduler_(e2e_scheduling_latency_microseconds|scheduling_algorithm_predicate_evaluation|scheduling_algorithm_priority_evaluation|scheduling_algorithm_preemption_evaluation|scheduling_algorithm_latency_microseconds|binding_latency_microseconds|scheduling_latency_seconds)",
		"apiserver_(request_count|request_latencies|request_latencies_summary|dropped_requests|storage_data_key_generation_latencies_microseconds|storage_transformation_failures_total|storage_transformation_latencies_microseconds|proxy_tunnel_sync_latency_secs)",
		"docker_(operations|operations_latency_microseconds|operations_errors|operations_timeout)",
		"reflector_(items_per_list|items_per_watch|list_duration_seconds|lists_total|short_watches_total|watch_duration_seconds|watches_total)",
		"etcd_(helper_cache_hit_count|helper_cache_miss_count|helper_cache_entry_count|request_cache_get_latencies_summary|request_cache_add_latencies_summary|request_latencies_summary)",
		"transformation_(transformation_latencies_microseconds|failures_total)",
		"network_plugin_operations_latency_microseconds|sync_proxy_rules_latency_microseconds|rest_client_request_latency_seconds",
	}
	cfg := &promcfg.ScrapeConfig{
		JobName:        "kube-apiserver",
		ScrapeInterval: model.Duration(15 * time.Second),
		HonorLabels:    false,
		ServiceDiscoveryConfig: promcfg.ServiceDiscoveryConfig{
			KubernetesSDConfigs: []*promcfg.KubernetesSDConfig{
				{
					Role: promcfg.KubernetesRoleEndpoint,
					NamespaceDiscovery: promcfg.KubernetesNamespaceDiscovery{
						Names: []string{ns},
					},
				},
			},
		},
		Scheme: "https",
		HTTPClientConfig: promcfg.HTTPClientConfig{
			TLSConfig: promcfg.TLSConfig{
				InsecureSkipVerify: false,
				ServerName:         "kubernetes",
				CAFile:             path.Join(volumeMounts.Path(prometheusContainerMain().Name, prometheusVolumeRootCA().Name), pki.CASignerCertMapKey),
			},
			BearerTokenFile: path.Join(volumeMounts.Path(prometheusContainerMain().Name, util.TokenMinterTokenVolume), util.TokenMinterTokenName),
		},
		RelabelConfigs: []*promcfg.RelabelConfig{
			{
				Action:       promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{"job"},
				TargetLabel:  "__tmp_prometheus_job_name",
			},
			{
				Action: promcfg.RelabelKeep,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_service_label_app",
				},
				Regex: promcfg.MustNewRegexp("kube-apiserver"),
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_endpoint_address_target_kind",
					"__meta_kubernetes_endpoint_address_target_name",
				},
				Separator:   ";",
				Regex:       promcfg.MustNewRegexp("Pod;(.*)"),
				Replacement: "${1}",
				TargetLabel: "pod",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_namespace",
				},
				TargetLabel: "namespace",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_service_name",
				},
				TargetLabel: "service",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_pod_name",
				},
				TargetLabel: "pod",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_pod_container_name",
				},
				TargetLabel: "container",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_service_name",
				},
				TargetLabel: "job",
				Replacement: "${1}",
			},
			{
				Action: promcfg.RelabelReplace,
				SourceLabels: model.LabelNames{
					"__meta_kubernetes_service_label_app",
				},
				TargetLabel: "job",
				Regex:       promcfg.MustNewRegexp("(.+)"),
				Replacement: "${1}",
			},
			{
				Action: promcfg.RelabelHashMod,
				SourceLabels: model.LabelNames{
					"__address__",
				},
				TargetLabel: "__tmp_hash",
				Modulus:     1,
			},
			{
				Action: promcfg.RelabelKeep,
				SourceLabels: model.LabelNames{
					"__tmp_hash",
				},
				Regex: promcfg.MustNewRegexp("0"),
			},
		},
		MetricRelabelConfigs: []*promcfg.RelabelConfig{
			{
				Action: promcfg.RelabelDrop,
				Regex:  promcfg.MustNewRegexp("apiserver_request_duration_seconds_bucket;(0.15|0.25|0.3|0.35|0.4|0.45|0.6|0.7|0.8|0.9|1.25|1.5|1.75|2.5|3|3.5|4.5|6|7|8|9|15|25|30|50)"),
				SourceLabels: model.LabelNames{
					"__name__",
					"le",
				},
			},
		},
	}

	var dropMetricCfgs []*promcfg.RelabelConfig
	for _, re := range dropMetrics {
		dropMetricCfgs = append(dropMetricCfgs, &promcfg.RelabelConfig{
			Action: promcfg.RelabelDrop,
			Regex:  promcfg.MustNewRegexp(re),
			SourceLabels: model.LabelNames{
				"__name__",
			},
		})
	}
	cfg.MetricRelabelConfigs = append(dropMetricCfgs, cfg.MetricRelabelConfigs...)

	return cfg

}

func guestClusterPrometheusWriteConfig(apiServerPort *int32) (*promcfg.RemoteWriteConfig, error) {
	apiPort := int32(config.DefaultAPIServerPort)
	if apiServerPort != nil {
		apiPort = *apiServerPort
	}
	remoteWriteURLStr := fmt.Sprintf("https://%s:%d/api/v1/namespaces/openshift-monitoring/services/https:prometheus-k8s:web/proxy/api/v1/write", manifests.KASService("").Name, apiPort)
	remoteWriteURL, err := url.Parse(remoteWriteURLStr)
	if err != nil {
		return nil, err
	}
	cfg := &promcfg.RemoteWriteConfig{
		URL: &promcfg.URL{URL: remoteWriteURL},
		HTTPClientConfig: promcfg.HTTPClientConfig{
			BearerTokenFile: path.Join(volumeMounts.Path(prometheusContainerMain().Name, util.TokenMinterTokenVolume), util.TokenMinterTokenName),
			TLSConfig: promcfg.TLSConfig{
				CAFile:     path.Join(volumeMounts.Path(prometheusContainerMain().Name, prometheusVolumeServiceCA().Name), "service-ca.crt"),
				ServerName: manifests.KASService("").Name,
			},
		},
	}
	return cfg, nil
}
