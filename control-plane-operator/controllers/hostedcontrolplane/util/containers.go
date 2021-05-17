package util

import (
	corev1 "k8s.io/api/core/v1"
)

func BuildContainer(container *corev1.Container, buildFn func(*corev1.Container)) corev1.Container {
	buildFn(container)
	return *container
}
