package configoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptPodMonitor(cpContext component.WorkloadContext, podMonitor *prometheusoperatorv1.PodMonitor) error {
	podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{cpContext.HCP.Namespace}}
	podMonitor.Spec.PodMetricsEndpoints[0].MetricRelabelConfigs = metrics.HostedClusterConfigOperatorRelabelConfigs(cpContext.MetricsSet)
	util.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], cpContext.HCP.Spec.ClusterID)

	// IBMCloudPlatform
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		if podMonitor.Annotations == nil {
			podMonitor.Annotations = map[string]string{}
		}
		podMonitor.Annotations["hypershift.openshift.io/metrics-service"] = "roks-metrics"
	}

	return nil
}
