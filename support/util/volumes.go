package util

import (
	corev1 "k8s.io/api/core/v1"
)

func BuildVolume(volume *corev1.Volume, buildFn func(*corev1.Volume)) corev1.Volume {
	buildFn(volume)
	return *volume
}
