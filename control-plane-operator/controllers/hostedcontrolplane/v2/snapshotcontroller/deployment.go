package snapshotcontroller

import (
	"strconv"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/omnicpo"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		for i, env := range c.Env {
			switch env.Name {
			case "OPERATOR_IMAGE_VERSION":
				c.Env[i].Value = cpContext.UserReleaseImageProvider.Version()
			case "OPERAND_IMAGE_VERSION":
				c.Env[i].Value = cpContext.UserReleaseImageProvider.Version()
			case "OPERAND_IMAGE":
				c.Env[i].Value = cpContext.ReleaseImageProvider.GetImage("csi-snapshot-controller")
			}
		}
		// We set this so cluster-csi-storage-controller operator knows which User ID to run the csi-snapshot-controller pods as.
		// This is needed when these pods are run on a management cluster that is non-OpenShift such as AKS.
		if cpContext.SetDefaultSecurityContext {
			c.Env = append(c.Env, corev1.EnvVar{Name: "RUN_AS_USER", Value: strconv.Itoa(int(cpContext.DefaultSecurityContextUID))})
		}
	})

	return nil
}

// omniCPOTransforms lists the drift adjustments a main-built CPO applies to the
// csi-snapshot-controller-operator Deployment when it drives an older control plane. See
// support/omnicpo.
func omniCPOTransforms() []omnicpo.Transform[*appsv1.Deployment] {
	return []omnicpo.Transform[*appsv1.Deployment]{
		{
			// --config arrived at 4.23; a pre-4.23 csi-snapshot-controller-operator rejects it.
			Reason: "pre-4.23 csi-snapshot-controller-operator rejects --config",
			When:   omnicpo.Below(config.Version423),
			Do:     omnicpo.StripArgs(ComponentName, "--config="),
		},
	}
}
