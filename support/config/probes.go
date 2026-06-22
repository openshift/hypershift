package config

import (
	corev1 "k8s.io/api/core/v1"
)

type LivenessProbes map[string]corev1.Probe

func (p LivenessProbes) ApplyTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		p.ApplyToContainer(c.Name, &podSpec.InitContainers[i])
	}
	for i, c := range podSpec.Containers {
		p.ApplyToContainer(c.Name, &podSpec.Containers[i])
	}
}

func (p LivenessProbes) ApplyToContainer(container string, c *corev1.Container) {
	if probe, ok := p[c.Name]; ok {
		c.LivenessProbe = &probe
	}
}

type ReadinessProbes map[string]corev1.Probe

func (p ReadinessProbes) ApplyTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		p.ApplyToContainer(c.Name, &podSpec.InitContainers[i])
	}
	for i, c := range podSpec.Containers {
		p.ApplyToContainer(c.Name, &podSpec.Containers[i])
	}
}

func (p ReadinessProbes) ApplyToContainer(container string, c *corev1.Container) {
	if probe, ok := p[c.Name]; ok {
		c.ReadinessProbe = &probe
	}
}

type StartupProbes map[string]corev1.Probe

func (p StartupProbes) ApplyTo(podSpec *corev1.PodSpec) {
	for i, c := range podSpec.InitContainers {
		p.ApplyToContainer(c.Name, &podSpec.InitContainers[i])
	}
	for i, c := range podSpec.Containers {
		p.ApplyToContainer(c.Name, &podSpec.Containers[i])
	}
}

func (p StartupProbes) ApplyToContainer(container string, c *corev1.Container) {
	if probe, ok := p[c.Name]; ok {
		c.StartupProbe = &probe
	}
}
