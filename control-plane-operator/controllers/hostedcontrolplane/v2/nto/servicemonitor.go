package nto

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	metricsServiceName = "node-tuning-operator"
)

func adaptServiceMonitor(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor) error {
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}

	if len(sm.Spec.Endpoints) == 0 {
		return fmt.Errorf("ServiceMonitor %s/%s has no endpoints defined", sm.Namespace, sm.Name)
	}

	if sm.Spec.Endpoints[0].TLSConfig == nil {
		return fmt.Errorf("ServiceMonitor %s/%s endpoint 0 has no TLSConfig", sm.Namespace, sm.Name)
	}

	sm.Spec.Endpoints[0].TLSConfig.ServerName = ptr.To(metricsServiceName + "." + sm.Namespace + ".svc")
	sm.Spec.Endpoints[0].MetricRelabelConfigs = metrics.NTORelabelConfigs(cpContext.MetricsSet)
	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], cpContext.HCP.Spec.ClusterID)

	return nil
}
