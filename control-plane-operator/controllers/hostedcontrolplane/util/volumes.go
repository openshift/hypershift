package util

import (
	corev1 "k8s.io/api/core/v1"
)

func BuildVolume(volume *corev1.Volume, buildFn func(*corev1.Volume)) corev1.Volume {
	buildFn(volume)
	return *volume
}

// ApplyVolume will add or update volume within volumes and return an
// array of volumes with the mutated volume.
func ApplyVolume(volumes []corev1.Volume, volume *corev1.Volume, buildFn func(*corev1.Volume)) []corev1.Volume {
	for _, existing := range volumes {
		if existing.Name == volume.Name {
			buildFn(&existing)
			return volumes
		}
	}
	buildFn(volume)
	return append(volumes, *volume)
}
