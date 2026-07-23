package aws

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName = "cloud-controller-manager"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		proxy.SetEnvVars(&c.Env)
	})
	return nil
}
