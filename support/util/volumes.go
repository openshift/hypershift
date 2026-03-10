package util

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func BuildVolume(volume *corev1.Volume, buildFn func(*corev1.Volume)) corev1.Volume {
	buildFn(volume)
	return *volume
}

func BuildProjectedVolume(volume *corev1.Volume, volumeProjection []corev1.VolumeProjection, buildFn func(*corev1.Volume, []corev1.VolumeProjection)) corev1.Volume {
	buildFn(volume, volumeProjection)
	return *volume
}

func DeploymentAddTrustBundleVolume(trustBundleConfigMap *corev1.LocalObjectReference, deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "trusted-ca",
		MountPath: "/etc/pki/tls/certs",
		ReadOnly:  true,
	})
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "trusted-ca",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: *trustBundleConfigMap,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "user-ca-bundle.pem"}},
			},
		},
	})
}

// DeploymentAddAWSCABundleVolume mounts the additionalTrustBundle ConfigMap and sets AWS_CA_BUNDLE
// on the first container. Unlike DeploymentAddTrustBundleVolume, this uses a non-conflicting mount
// path so it does not replace the system CA directory. This is needed for third-party binaries
// (aws-cloud-controller-manager, capi-provider, ingress-operator) that rely on system CAs and use
// the AWS SDK's AWS_CA_BUNDLE mechanism to append additional trusted CAs.
func DeploymentAddAWSCABundleVolume(trustBundleConfigMap *corev1.LocalObjectReference, deployment *appsv1.Deployment) {
	const (
		volumeName = "aws-ca-bundle"
		mountPath  = "/etc/pki/ca-trust/extracted/hypershift"
		fileName   = "user-ca-bundle.pem"
	)

	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: mountPath,
		ReadOnly:  true,
	})
	deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
		Name:  "AWS_CA_BUNDLE",
		Value: mountPath + "/" + fileName,
	})
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: *trustBundleConfigMap,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: fileName}},
			},
		},
	})
}

func UpdateVolume(name string, volumes []corev1.Volume, update func(v *corev1.Volume)) {
	for i, v := range volumes {
		if v.Name == name {
			update(&volumes[i])
		}
	}
}
