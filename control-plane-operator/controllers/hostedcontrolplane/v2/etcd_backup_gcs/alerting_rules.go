package etcdbackupgcs

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptAlertingRules(cpContext component.WorkloadContext, r *prometheusoperatorv1.PrometheusRule) error {
	for gi := range r.Spec.Groups {
		for ri := range r.Spec.Groups[gi].Rules {
			rule := &r.Spec.Groups[gi].Rules[ri]
			util.ApplyClusterIDLabelToRecordingRule(rule, cpContext.HCP.Spec.ClusterID)
		}
	}
	return nil
}
