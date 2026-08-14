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

func (s ResourcesSpec) ApplyRequestsOverrideTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		if res, ok := s[c.Name]; ok {
			for name, value := range res.Requests {
				podSpec.InitContainers[i].Resources.Requests[name] = value
			}
		}
	}
	for i, c := range podSpec.Containers {
		if res, ok := s[c.Name]; ok {
			for name, value := range res.Requests {
				podSpec.Containers[i].Resources.Requests[name] = value
			}
		}
	}
}

type ResourceOverrides map[string]ResourcesSpec

func (o ResourceOverrides) ApplyRequestsTo(name string, podSpec *corev1.PodSpec) {
	if spec, exists := o[name]; exists {
		spec.ApplyRequestsOverrideTo(podSpec)
	}
}
