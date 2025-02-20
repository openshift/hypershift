package snapshotcontroller

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		for i, env := range c.Env {
			switch env.Name {
			case "OPERATOR_IMAGE_VERSION":
				c.Env[i].Value = cpContext.UserReleaseImageProvider.Version()
			case "OPERAND_IMAGE_VERSION":
				c.Env[i].Value = cpContext.UserReleaseImageProvider.Version()
			case "OPERAND_IMAGE":
				c.Env[i].Value = cpContext.ReleaseImageProvider.GetImage("csi-snapshot-controller")
			case "WEBHOOK_IMAGE":
				c.Env[i].Value = cpContext.ReleaseImageProvider.GetImage("csi-snapshot-validation-webhook")
			}
		}
		// We set this so cluster-csi-storage-controller operator knows which User ID to run the csi-snapshot-controller and csi-snapshot-webhook pods as.
		// This is needed when these pods are run on a management cluster that is non-OpenShift such as AKS.
		if cpContext.SetDefaultSecurityContext {
			c.Env = append(c.Env, corev1.EnvVar{Name: "RUN_AS_USER", Value: "1001"})
		}
	})

	return nil
}
