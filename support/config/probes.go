package config

import (
	corev1 "k8s.io/api/core/v1"
)

type LivenessProbes map[string]corev1.Probe

func (p LivenessProbes) ApplyTo(podSpec *corev1.PodSpec) {
	for i := range podSpec.InitContainers {
		p.ApplyToContainer(&podSpec.InitContainers[i])
	}
	for i := range podSpec.Containers {
		p.ApplyToContainer(&podSpec.Containers[i])
	}
}

func (p LivenessProbes) ApplyToContainer(c *corev1.Container) {
	if probe, ok := p[c.Name]; ok {
		c.LivenessProbe = &probe
	}
}

type ReadinessProbes map[string]corev1.Probe

func (p ReadinessProbes) ApplyTo(podSpec *corev1.PodSpec) {
	for i := range podSpec.InitContainers {
		p.ApplyToContainer(&podSpec.InitContainers[i])
	}
	for i := range podSpec.Containers {
		p.ApplyToContainer(&podSpec.Containers[i])
	}
}

func (p ReadinessProbes) ApplyToContainer(c *corev1.Container) {
	if probe, ok := p[c.Name]; ok {
		c.ReadinessProbe = &probe
	}
}
