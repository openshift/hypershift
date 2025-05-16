package controlplaneoperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	"sigs.k8s.io/controller-runtime/pkg/client"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func (cpo *ControlPlaneOperatorOptions) adaptPodMonitor(cpContext component.WorkloadContext, podMonitor *prometheusoperatorv1.PodMonitor) error {
	podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{cpContext.HCP.Namespace}}
	podMonitor.Spec.PodMetricsEndpoints[0].MetricRelabelConfigs = metrics.ControlPlaneOperatorRelabelConfigs(cpContext.MetricsSet)
	util.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], cpContext.HCP.Spec.ClusterID)

	if podMonitor.Annotations == nil {
		podMonitor.Annotations = map[string]string{}
	}
	podMonitor.Annotations[util.HostedClusterAnnotation] = client.ObjectKeyFromObject(cpo.HostedCluster).String()

	return nil
}
