package aws

import (
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	containerName       = "cloud-controller-manager"
	trustedCAVolumeName = "trusted-ca"
	caBundleKey         = "ca-bundle.crt"
	caBundlePath        = "ca-bundle.pem"
	caDir               = "/etc/pki/ca-trust/custom"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cpContext.HCP.Spec.AdditionalTrustBundle == nil {
		return nil
	}

	// TODO(CNTRLPLANE-625): Consider adding a config hash annotation to trigger
	// rolling restart when the CA bundle content changes. The AWS SDK caches TLS
	// config at connection pool creation, so CA rotation without a pod restart
	// may not take effect until the next pod lifecycle event.
	// TODO(CNTRLPLANE-625): Other AWS components (EBS CSI driver, CNCC, Ingress,
	// Image Registry) also make AWS API calls and may need AWS_CA_BUNDLE support.
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildTrustedCAVolume(cpContext.HCP.Namespace))

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

func buildTrustedCAVolume(namespace string) corev1.Volume {
	return corev1.Volume{
		Name: trustedCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: cpomanifests.UserCAConfigMap(namespace).Name,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  caBundleKey,
						Path: caBundlePath,
					},
				},
				Optional: ptr.To(true),
			},
		},
	}
}
