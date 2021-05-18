package util

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
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
