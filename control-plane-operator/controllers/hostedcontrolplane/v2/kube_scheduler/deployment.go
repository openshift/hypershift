package scheduler

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	featureGates, err := config.FeatureGatesFromConfigMap(cpContext.Context, cpContext.Client, cpContext.HCP.Namespace)
	if err != nil {
		return err
	}
	configuration := cpContext.HCP.Spec.Configuration
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = config.AppendTLSArgs(c.Args, configuration.GetTLSSecurityProfile())
		if util.StringListContains(cpContext.HCP.Annotations[hyperv1.DisableProfilingAnnotation], ComponentName) {
			c.Args = append(c.Args, "--profiling=false")
		}
		for _, f := range featureGates {
			c.Args = append(c.Args, fmt.Sprintf("--feature-gates=%s", f))
		}
		if configuration != nil && configuration.Scheduler != nil && len(configuration.Scheduler.Policy.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-map=%s", configuration.Scheduler.Policy.Name))
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-namespace=%s", cpContext.HCP.Namespace))
		}
	})

	return nil
}
