package machineapprover

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--machine-namespace=%s", hcp.Namespace))
	})

	return nil
}
