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

	tlsArgs, err := config.TLSArgs(configuration.GetTLSSecurityProfile())
	if err != nil {
		return err
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}

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
		c.Args = append(c.Args, fmt.Sprintf("--v=%d", resolveSchedulerVerbosity(cpContext.HCP)))
	})

	return nil
}

func resolveSchedulerVerbosity(hcp *hyperv1.HostedControlPlane) int {
	var level hyperv1.LogLevel
	if hcp.Spec.OperatorConfiguration != nil {
		level = hcp.Spec.OperatorConfiguration.KubeScheduler.LogLevel
	}
	return util.LogLevelToKlogVerbosity(level)
}
