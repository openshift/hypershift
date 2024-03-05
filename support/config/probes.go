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
		if probe.TimeoutSeconds == 0 {
			probe.TimeoutSeconds = 30 // default is 1s, but we want to be more patient
		}
		if probe.PeriodSeconds == 0 {
			probe.PeriodSeconds = 60 // default is 10s, but we want check liveness less frequently
		}
		if probe.InitialDelaySeconds == 0 {
			probe.InitialDelaySeconds = 60 // allow time for pod to start and stabilize
		}
		if probe.FailureThreshold == 0 {
			probe.FailureThreshold = 3 // explicitly set kube default to prevent no-op loop
		}
		if probe.SuccessThreshold == 0 {
			probe.SuccessThreshold = 1 // explicitly set kube default to prevent no-op loop
		}
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
		if probe.TimeoutSeconds == 0 {
			probe.TimeoutSeconds = 5 // default is 1s, but we want to be more patient
		}
		if probe.PeriodSeconds == 0 {
			probe.PeriodSeconds = 10 // explicitly set kube default to prevent no-op loop
		}
		probe.InitialDelaySeconds = 0 // never delay for readiness probe
		if probe.FailureThreshold == 0 {
			probe.FailureThreshold = 3 // explicitly set kube default to prevent no-op loop
		}
		if probe.SuccessThreshold == 0 {
			probe.SuccessThreshold = 1 // explicitly set kube default to prevent no-op loop
		}
		c.ReadinessProbe = &probe
	}
}
