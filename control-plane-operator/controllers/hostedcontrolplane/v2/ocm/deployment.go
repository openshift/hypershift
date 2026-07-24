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
	// Only override verbosity when explicitly configured; preserve klog default otherwise.
	if v, ok := resolveOCMVerbosity(cpContext.HCP); ok {
		podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Args = append(c.Args, fmt.Sprintf("--v=%d", v))
		})
	}
	return nil
}

// resolveOCMVerbosity returns the klog verbosity for openshift-controller-manager
// only when explicitly configured via OperatorConfiguration. Returns false when
// no LogLevel is set, preserving the klog default (0) for existing clusters.
func resolveOCMVerbosity(hcp *hyperv1.HostedControlPlane) (int, bool) {
	if hcp.Spec.OperatorConfiguration != nil &&
		hcp.Spec.OperatorConfiguration.OpenShiftControllerManager.LogLevel != nil {
		return util.LogLevelToKlogVerbosity(
			hcp.Spec.OperatorConfiguration.OpenShiftControllerManager.LogLevel), true
	}
	return 0, false
}
