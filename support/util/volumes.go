package util

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func BuildVolume(volume *corev1.Volume, buildFn func(*corev1.Volume)) corev1.Volume {
	buildFn(volume)
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
