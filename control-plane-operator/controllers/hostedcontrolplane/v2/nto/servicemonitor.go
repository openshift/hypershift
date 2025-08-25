package nto

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	metricsServiceName = "node-tuning-operator"
)

func adaptServiceMonitor(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor) error {
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}

	sm.Spec.Endpoints[0].Scheme = "http"
	sm.Spec.Endpoints[0].TLSConfig = nil
	sm.Spec.Endpoints[0].MetricRelabelConfigs = metrics.NTORelabelConfigs(cpContext.MetricsSet)
	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], cpContext.HCP.Spec.ClusterID)

	return nil
}
