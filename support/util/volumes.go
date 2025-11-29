package util

import (
	"slices"

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

func UpdateVolume(name string, volumes []corev1.Volume, update func(v *corev1.Volume)) {
	for i, v := range volumes {
		if v.Name == name {
			update(&volumes[i])
		}
	}
}

func RemovePodVolume(name string, podSpec *corev1.PodSpec) {
	podSpec.Volumes = slices.DeleteFunc(podSpec.Volumes, func(v corev1.Volume) bool {
		return v.Name == name
	})
}
