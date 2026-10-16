package ocm

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--v=%d", resolveOCMVerbosity(cpContext.HCP)))
	})
	return nil
}

func resolveOCMVerbosity(hcp *hyperv1.HostedControlPlane) int {
	var level hyperv1.LogLevel
	if hcp.Spec.OperatorConfiguration != nil {
		level = hcp.Spec.OperatorConfiguration.OpenShiftControllerManager.LogLevel
	}
	return util.LogLevelToKlogVerbosity(level)
}
