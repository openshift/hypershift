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

// DeploymentAddAWSCABundleVolume creates a combined CA bundle containing both the system CAs from
// the container image and the user-provided additionalTrustBundle CAs, then sets AWS_CA_BUNDLE on
// the first container. An init container concatenates /etc/pki/tls/certs/ca-bundle.crt (system CAs)
// with the user CAs into a single file. This is necessary because the AWS SDK replaces the default
// system CA bundle when AWS_CA_BUNDLE is set, rather than appending to it.
// The initContainerImage should be a RHEL-based image that has /bin/sh and cat available
// (e.g. the control-plane-operator image).
func DeploymentAddAWSCABundleVolume(trustBundleConfigMap *corev1.LocalObjectReference, deployment *appsv1.Deployment, initContainerImage string) {
	const (
		userCAVolumeName     = "user-ca-bundle"
		combinedCAVolumeName = "aws-ca-bundle"
		userCAMountPath      = "/user-ca"
		combinedCAMountPath  = "/etc/pki/ca-trust/extracted/hypershift"
		userCAFileName       = "user-ca-bundle.pem"
		combinedCAFileName   = "combined-ca-bundle.pem"
		systemCABundlePath   = "/etc/pki/tls/certs/ca-bundle.crt"
		initContainerName    = "setup-aws-ca-bundle"
	)

	// Volume for user CAs from additionalTrustBundle ConfigMap.
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: userCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: *trustBundleConfigMap,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: userCAFileName}},
			},
		},
	})

	// EmptyDir volume for the combined (system + user) CA bundle.
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: combinedCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	// Init container concatenates system CAs with user CAs into the combined bundle.
	deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers, corev1.Container{
		Name:  initContainerName,
		Image: initContainerImage,
		Command: []string{"/bin/sh", "-c",
			"cat " + systemCABundlePath + " " + userCAMountPath + "/" + userCAFileName +
				" > " + combinedCAMountPath + "/" + combinedCAFileName},
		VolumeMounts: []corev1.VolumeMount{
			{Name: userCAVolumeName, MountPath: userCAMountPath, ReadOnly: true},
			{Name: combinedCAVolumeName, MountPath: combinedCAMountPath},
		},
	})

	// Mount the combined CA bundle in the main container.
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      combinedCAVolumeName,
		MountPath: combinedCAMountPath,
		ReadOnly:  true,
	})

	// Point AWS_CA_BUNDLE to the combined CA file so the AWS SDK trusts both system and user CAs.
	deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
		Name:  "AWS_CA_BUNDLE",
		Value: combinedCAMountPath + "/" + combinedCAFileName,
	})
}

func UpdateVolume(name string, volumes []corev1.Volume, update func(v *corev1.Volume)) {
	for i, v := range volumes {
		if v.Name == name {
			update(&volumes[i])
		}
	}
}
