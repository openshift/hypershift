package config

import (
	corev1 "k8s.io/api/core/v1"
)

type ResourcesSpec map[string]corev1.ResourceRequirements

func (s ResourcesSpec) ApplyTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		if res, ok := s[c.Name]; ok {
			podSpec.InitContainers[i].Resources = res
		}
	}
	for i, c := range podSpec.Containers {
		if res, ok := s[c.Name]; ok {
			podSpec.Containers[i].Resources = res
		}
	}
}
