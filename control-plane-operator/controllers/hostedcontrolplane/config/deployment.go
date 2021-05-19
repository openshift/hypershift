package config

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/pointer"
)

type DeploymentConfig struct {
	Replicas         int                 `json:"replicas"`
	Scheduling       Scheduling          `json:"scheduling"`
	AdditionalLabels AdditionalLabels    `json:"additionalLabels"`
	SecurityContexts SecurityContextSpec `json:"securityContexts"`
	LivenessProbes   LivenessProbes      `json:"livenessProbes"`
	ReadinessProbes  ReadinessProbes     `json:"readinessProbes"`
	Resources        ResourcesSpec       `json:"resources"`
}

func (c *DeploymentConfig) ApplyTo(deployment *appsv1.Deployment) {
	deployment.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	c.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
}
