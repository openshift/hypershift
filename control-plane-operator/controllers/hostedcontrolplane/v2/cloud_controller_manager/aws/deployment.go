package aws

import (
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName       = "cloud-controller-manager"
	trustedCAVolumeName = "trusted-ca"
	caBundleKey         = "ca-bundle.crt"
	caBundlePath        = "ca-bundle.pem"
	caDir               = "/etc/pki/ca-trust/extracted/pem"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cpContext.HCP.Spec.AdditionalTrustBundle == nil {
		return nil
	}

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildTrustedCAVolume(cpContext))

	util.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      trustedCAVolumeName,
			MountPath: caDir,
			ReadOnly:  true,
		})
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "AWS_CA_BUNDLE",
			Value: caDir + "/" + caBundlePath,
		})
	})

	return nil
}

func buildTrustedCAVolume(cpContext component.WorkloadContext) corev1.Volume {
	return corev1.Volume{
		Name: trustedCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: cpomanifests.UserCAConfigMap(cpContext.HCP.Namespace).Name,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  caBundleKey,
						Path: caBundlePath,
					},
				},
			},
		},
	}
}
