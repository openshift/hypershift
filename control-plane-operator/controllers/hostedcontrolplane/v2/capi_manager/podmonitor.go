package capimanager

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// adaptPodMonitor scopes the cluster-api PodMonitor to the hosted control plane namespace and
// labels the scraped series with the cluster ID, like the cluster-autoscaler PodMonitor.
// The CAPI manager is reconciled by the HyperShift Operator without an owner reference, so the
// hosted cluster annotation is also set to route PodMonitor events back to the HostedCluster.
func adaptPodMonitor(cpContext component.WorkloadContext, podMonitor *prometheusoperatorv1.PodMonitor) error {
	if len(podMonitor.Spec.PodMetricsEndpoints) == 0 {
		return fmt.Errorf("podmonitor %s has no pod metrics endpoints", podMonitor.Name)
	}

	podMonitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{MatchNames: []string{cpContext.HCP.Namespace}}
	util.ApplyClusterIDLabelToPodMonitor(&podMonitor.Spec.PodMetricsEndpoints[0], cpContext.HCP.Spec.ClusterID)

	if hostedCluster := cpContext.HCP.Annotations[k8sutil.HostedClusterAnnotation]; hostedCluster != "" {
		if podMonitor.Annotations == nil {
			podMonitor.Annotations = map[string]string{}
		}
		podMonitor.Annotations[k8sutil.HostedClusterAnnotation] = hostedCluster
	}

	return nil
}
