package scheduler

import (
	_ "embed"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptServiceMonitor(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor) error {
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{cpContext.HCP.Namespace},
	}
	for i := range len(sm.Spec.Endpoints) {
		util.ApplyClusterIDLabel(&sm.Spec.Endpoints[i], cpContext.HCP.Spec.ClusterID)
	}
	return nil
}
