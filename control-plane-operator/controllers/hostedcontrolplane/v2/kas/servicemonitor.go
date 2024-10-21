package kas

import (
	_ "embed"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptServiceMonitor(cpContext component.ControlPlaneContext, sm *prometheusoperatorv1.ServiceMonitor) error {
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{cpContext.HCP.Namespace},
	}
	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], cpContext.HCP.Spec.ClusterID)

	return nil
}

func adaptRecordingRules(cpContext component.ControlPlaneContext, r *prometheusoperatorv1.PrometheusRule) error {
	for gi := range r.Spec.Groups {
		for ri := range r.Spec.Groups[gi].Rules {
			rule := &r.Spec.Groups[gi].Rules[ri]
			util.ApplyClusterIDLabelToRecordingRule(rule, cpContext.HCP.Spec.ClusterID)
		}
	}
	return nil
}
