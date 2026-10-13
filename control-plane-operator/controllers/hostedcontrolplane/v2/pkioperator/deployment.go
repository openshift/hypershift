package pkioperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func (p *pkiOperator) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		proxy.SetEnvVars(&c.Env)

		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "HOSTED_CONTROL_PLANE_NAME",
			Value: cpContext.HCP.Name,
		})
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "CERT_ROTATION_SCALE",
			Value: p.certRotationScale.String(),
		})
	})

	return nil
}
