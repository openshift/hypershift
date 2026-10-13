package nto

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "RELEASE_VERSION",
			Value: cpContext.UserReleaseImageProvider.Version(),
		})

		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "CLUSTER_NODE_TUNED_IMAGE",
			Value: cpContext.UserReleaseImageProvider.GetImage(ComponentName),
		})
	})

	return nil
}
