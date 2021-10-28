package util

import (
	"path"
	"reflect"

	corev1 "k8s.io/api/core/v1"
)

func BuildContainer(container *corev1.Container, buildFn func(*corev1.Container)) corev1.Container {
	buildFn(container)
	return *container
}

// AvailabilityProberImageName is the name under which components can find the availability prober
// image in the release image.
const AvailabilityProberImageName = "availability-prober"

func AvailabilityProber(target string, image string, spec *corev1.PodSpec) {
	availabilityProberContainer := corev1.Container{
		Name:            "availability-prober",
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command: []string{
			"/usr/bin/availability-prober",
			"--target",
			target,
		},
	}
	if len(spec.InitContainers) == 0 || spec.InitContainers[0].Name != "availability-prober" {
		spec.InitContainers = append([]corev1.Container{{}}, spec.InitContainers...)
	}
	if !reflect.DeepEqual(spec.InitContainers[0], availabilityProberContainer) {
		spec.InitContainers[0] = availabilityProberContainer
	}
}

const (
	TokenMinterImageName   = "token-minter"
	TokenMinterTokenVolume = "token-minter-token"
	TokenMinterTokenName   = "token"
)

func TokenMinterInit(image, saNamespace, saName, audience, kubeconfigVolumeName, kubeconfigPath string, spec *corev1.PodSpec) {
	tokenMinterContainer := corev1.Container{
		Name:            "token-minter",
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command: []string{
			"/usr/bin/token-minter",
			"--kubeconfig",
			path.Join("/etc/kubernetes/kubeconfig", kubeconfigPath),
			"--service-account-name",
			saName,
			"--service-account-namespace",
			saNamespace,
			"--token-audience",
			audience,
			"--token-file",
			path.Join("/var/run/secrets/openshift/serviceaccount", TokenMinterTokenName),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "token-minter-token",
				MountPath: "/var/run/secrets/openshift/serviceaccount",
			},
			{
				Name:      kubeconfigVolumeName,
				MountPath: "/etc/kubernetes/kubeconfig",
			},
		},
	}

	tokenVolume := corev1.Volume{
		Name: TokenMinterTokenVolume,
	}
	tokenVolume.VolumeSource.EmptyDir = &corev1.EmptyDirVolumeSource{}

	tokenMinterContainerIndex := -1
	for i, c := range spec.InitContainers {
		if c.Name == "token-minter" {
			tokenMinterContainerIndex = i
			break
		}
	}
	if tokenMinterContainerIndex == -1 {
		spec.InitContainers = append(spec.InitContainers, tokenMinterContainer)
	} else {
		spec.InitContainers[tokenMinterContainerIndex] = tokenMinterContainer
	}

	tokenMinterVolumeIndex := -1
	for i, v := range spec.Volumes {
		if v.Name == TokenMinterTokenVolume {
			tokenMinterVolumeIndex = i
			break
		}
	}
	if tokenMinterVolumeIndex == -1 {
		spec.Volumes = append(spec.Volumes, tokenVolume)
	} else {
		spec.Volumes[tokenMinterVolumeIndex] = tokenVolume
	}
}
