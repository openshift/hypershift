package config

import (
	corev1 "k8s.io/api/core/v1"
)

type SecurityContextSpec map[string]corev1.SecurityContext

func (s SecurityContextSpec) ApplyTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		s.ApplyToContainer(c.Name, &podSpec.InitContainers[i])
	}
	for i, c := range podSpec.Containers {
		s.ApplyToContainer(c.Name, &podSpec.Containers[i])
	}
}

func (s SecurityContextSpec) ApplyToContainer(name string, c *corev1.Container) {
	if ctx, ok := s[name]; ok {
		c.SecurityContext = &ctx
	}
}
