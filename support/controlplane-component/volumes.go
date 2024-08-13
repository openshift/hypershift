package controlplanecomponent

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

type Volumes map[string]Volume

type Volume struct {
	Source corev1.VolumeSource
	Mounts map[string]string
}

func (v Volumes) ApplyTo(podsSpec *corev1.PodSpec) {
	containerVolumeMounts := make(map[string][]corev1.VolumeMount)
	volumes := make([]corev1.Volume, 0, len(v))
	for volumeName, volumeOpts := range v {
		volume := corev1.Volume{
			Name:         volumeName,
			VolumeSource: volumeOpts.Source,
		}
		volumes = append(volumes, volume)

		for containerName, mountPath := range volumeOpts.Mounts {
			containerVolumeMounts[containerName] = append(containerVolumeMounts[containerName], corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountPath,
			})
		}
	}
	// Sort Volumes to get a reproducible list
	slices.SortFunc(volumes, func(a, b corev1.Volume) int {
		return strings.Compare(a.Name, b.Name)
	})
	podsSpec.Volumes = volumes

	for i, c := range podsSpec.Containers {
		volumeMounts, ok := containerVolumeMounts[c.Name]
		if ok {
			// Sort to get a reproducible list
			slices.SortFunc(volumeMounts, func(a, b corev1.VolumeMount) int {
				return strings.Compare(a.Name, b.Name)
			})
			podsSpec.Containers[i].VolumeMounts = volumeMounts
		}
	}
	for i, c := range podsSpec.InitContainers {
		volumeMounts, ok := containerVolumeMounts[c.Name]
		if ok {
			// Sort to get a reproducible list
			slices.SortFunc(volumeMounts, func(a, b corev1.VolumeMount) int {
				return strings.Compare(a.Name, b.Name)
			})
			podsSpec.InitContainers[i].VolumeMounts = volumeMounts
		}
	}
}

func (v Volumes) Path(containerName, volumeName string) string {
	volume, ok := v[volumeName]
	if !ok {
		panic(fmt.Sprintf("invalid volume %s for container %s used when looking for mount", volumeName, containerName))
	}
	mountPath, ok := volume.Mounts[containerName]
	if !ok {
		panic(fmt.Sprintf("invalid pod container %s used when looking for mount", containerName))
	}
	return mountPath
}

func ConfigMapVolumeSource(name string) corev1.VolumeSource {
	volumeSource := corev1.VolumeSource{}
	volumeSource.ConfigMap = &corev1.ConfigMapVolumeSource{}
	volumeSource.ConfigMap.Name = name
	return volumeSource
}

func SecretVolumeSource(name string) corev1.VolumeSource {
	volumeSource := corev1.VolumeSource{}
	volumeSource.Secret = &corev1.SecretVolumeSource{
		SecretName:  name,
		DefaultMode: ptr.To[int32](0640),
	}
	return volumeSource
}
