package metricsproxy

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	metricsSet := cpContext.MetricsSet
	if metricsSet == "" {
		metricsSet = metrics.MetricsSetAll
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			"--metrics-set", string(metricsSet),
			"--authorized-sa", "system:serviceaccount:openshift-monitoring:prometheus-k8s",
		)
	})
	return nil
}
