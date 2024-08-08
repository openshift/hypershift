package util

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

type ContainerVolumeMounts map[string]string
type PodVolumeMounts map[string]ContainerVolumeMounts

func (m PodVolumeMounts) Path(container, volume string) string {
	containerMounts, ok := m[container]
	if !ok {
		panic(fmt.Sprintf("invalid pod container %s used when looking for mount", container))
	}
	mountPath, ok := containerMounts[volume]
	if !ok {
		panic(fmt.Sprintf("invalid volume %s for container %s used when looking for mount", volume, container))
	}
	return mountPath
}

func (m PodVolumeMounts) ContainerMounts(container string) []corev1.VolumeMount {
	result := []corev1.VolumeMount{}
	containerMounts, ok := m[container]
	if !ok {
		return result
	}
	// Sort by volume name to get a reproducible list
	volumeNames := make([]string, 0, len(containerMounts))
	for name := range containerMounts {
		volumeNames = append(volumeNames, name)
	}
	sort.Strings(volumeNames)
	for _, name := range volumeNames {
		result = append(result, corev1.VolumeMount{
			Name:      name,
			MountPath: containerMounts[name],
		})
	}
	return result
}

type Volumes map[string]Volume

type Volume struct {
	corev1.VolumeSource
	VolumeMounts ContainerVolumeMounts
}

func (v Volumes) ApplyTo(podsSpec *corev1.PodSpec) {
	containerVolumeMounts := make(map[string][]corev1.VolumeMount)
	for volumeName, volumeOpts := range v {
		volume := corev1.Volume{
			Name:         volumeName,
			VolumeSource: volumeOpts.VolumeSource,
		}
		podsSpec.Volumes = append(podsSpec.Volumes, volume)

		for containerName, mountPath := range volumeOpts.VolumeMounts {
			containerVolumeMounts[containerName] = append(containerVolumeMounts[containerName], corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountPath,
			})
		}
	}
	// Sort Volumes to get a reproducible list
	slices.SortFunc(podsSpec.Volumes, func(a, b corev1.Volume) int {
		return strings.Compare(a.Name, b.Name)
	})

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
	mountPath, ok := volume.VolumeMounts[containerName]
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
