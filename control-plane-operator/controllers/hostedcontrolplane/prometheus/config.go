package prometheus

import (
	"path"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	promcfg "github.com/prometheus/prometheus/config"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const prometheusConfigFileName = "prometheus.yml"

func ReconcileConfiguration(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	cfg := buildPrometheusConfig(cm.Namespace)

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[prometheusConfigFileName] = string(cfg.String())
	ownerRef.ApplyTo(cm)
	return nil
}

func buildPrometheusConfig(ns string) *promcfg.Config {
	return &promcfg.Config{
		GlobalConfig: promcfg.GlobalConfig{
			ScrapeInterval: model.Duration(15 * time.Second),
		},
		ScrapeConfigs: []*promcfg.ScrapeConfig{
			kubeAPIServerScrapeConfig(ns),
		},
	}
}

func kubeAPIServerScrapeConfig(ns string) *promcfg.ScrapeConfig {
	metricsToKeep := []string{
		"apiserver_request_total",
		"apiserver_request_duration_seconds",
		"etcd_object_counts",
	}
	return &promcfg.ScrapeConfig{
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
				SourceLabels: model.LabelNames{
					"__name__",
				},
				Regex:  promcfg.MustNewRegexp("(" + strings.Join(metricsToKeep, "|") + ")"),
				Action: promcfg.RelabelKeep,
			},
		},
	}
}
