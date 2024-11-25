package scheduler

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	configuration := cpContext.HCP.Spec.Configuration
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if tlsMinVersion := config.MinTLSVersion(configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
		if util.StringListContains(cpContext.HCP.Annotations[hyperv1.DisableProfilingAnnotation], ComponentName) {
			c.Args = append(c.Args, "--profiling=false")
		}
		for _, f := range config.FeatureGates(configuration.GetFeatureGateSelection()) {
			c.Args = append(c.Args, fmt.Sprintf("--feature-gates=%s", f))
		}
		if configuration != nil && configuration.Scheduler != nil && len(configuration.Scheduler.Policy.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-map=%s", configuration.Scheduler.Policy.Name))
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-namespace=%s", cpContext.HCP.Namespace))
		}
	})

	return nil
}
