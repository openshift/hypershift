package config

import (
	corev1 "k8s.io/api/core/v1"
)

type Scheduling struct {
	Affinity      *corev1.Affinity    `json:"affinity,omitempty"`
	Tolerations   []corev1.Toleration `json:"tolerations,omitempty"`
	PriorityClass string              `json:"priorityClass"`
}

func (s *Scheduling) ApplyTo(podSpec *corev1.PodSpec) {
	podSpec.Affinity = s.Affinity
	podSpec.Tolerations = s.Tolerations
	podSpec.PriorityClassName = s.PriorityClass
}
