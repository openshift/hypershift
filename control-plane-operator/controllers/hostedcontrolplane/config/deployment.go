package config

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	clusterIDLabel    = "clusterID"
	restartAnnotation = "hypershift.openshift.io/restartedAt"
)

type DeploymentConfig struct {
	Replicas              int                   `json:"replicas"`
	Scheduling            Scheduling            `json:"scheduling"`
	AdditionalLabels      AdditionalLabels      `json:"additionalLabels"`
	AdditionalAnnotations AdditionalAnnotations `json:"additionalAnnotations"`
	SecurityContexts      SecurityContextSpec   `json:"securityContexts"`
	LivenessProbes        LivenessProbes        `json:"livenessProbes"`
	ReadinessProbes       ReadinessProbes       `json:"readinessProbes"`
	Resources             ResourcesSpec         `json:"resources"`
}

func (c *DeploymentConfig) ApplyTo(deployment *appsv1.Deployment) {
	deployment.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	c.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.AdditionalAnnotations.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
}

func (c *DeploymentConfig) ApplyToDaemonSet(daemonset *appsv1.DaemonSet) {
	// replicas is not used for DaemonSets
	c.Scheduling.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.AdditionalAnnotations.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
}

func ApplyWorkloadConfig(workloadConfig *hyperv1.WorkloadConfiguration, deploymentConfig *DeploymentConfig, deploymentName string) {
	if workloadConfig == nil {
		return
	}
	if workloadConfig.Spec.Labels != nil {
		if deploymentConfig.AdditionalLabels == nil {
			deploymentConfig.AdditionalLabels = AdditionalLabels{}
		}
		for k, v := range workloadConfig.Spec.Labels {
			deploymentConfig.AdditionalLabels[k] = v
		}
	}
	if workloadConfig.Spec.Annotations != nil {
		if deploymentConfig.AdditionalAnnotations == nil {
			deploymentConfig.AdditionalAnnotations = AdditionalAnnotations{}
		}
		for k, v := range workloadConfig.Spec.Annotations {
			deploymentConfig.AdditionalAnnotations[k] = v
		}
	}
	for _, deploymentCfg := range workloadConfig.Spec.Deployments {
		if deploymentCfg.Name == deploymentName {
			if deploymentCfg.Labels != nil {
				if deploymentConfig.AdditionalLabels == nil {
					deploymentConfig.AdditionalLabels = AdditionalLabels{}
				}
				for k, v := range deploymentCfg.Labels {
					deploymentConfig.AdditionalLabels[k] = v
				}
			}
			if deploymentCfg.Annotations != nil {
				if deploymentConfig.AdditionalAnnotations == nil {
					deploymentConfig.AdditionalAnnotations = AdditionalAnnotations{}
				}
				for k, v := range deploymentCfg.Annotations {
					deploymentConfig.AdditionalAnnotations[k] = v
				}
			}
			if deploymentCfg.Affinity != nil {
				deploymentConfig.Scheduling.Affinity = deploymentCfg.Affinity
			}
			if deploymentCfg.Tolerations != nil {
				deploymentConfig.Scheduling.Tolerations = deploymentCfg.Tolerations
			}
			if deploymentCfg.PriorityClassName != nil {
				deploymentConfig.Scheduling.PriorityClass = *deploymentCfg.PriorityClassName
			}
			for _, containerCfg := range deploymentCfg.Containers {
				if containerCfg.Resources != nil {
					deploymentConfig.Resources[containerCfg.Name] = *containerCfg.Resources
				}
				if containerCfg.SecurityContext != nil {
					deploymentConfig.SecurityContexts[containerCfg.Name] = *containerCfg.SecurityContext
				}
				if containerCfg.LivenessProbe != nil {
					deploymentConfig.LivenessProbes[containerCfg.Name] = *containerCfg.LivenessProbe
				}
				if containerCfg.ReadinessProbe != nil {
					deploymentConfig.ReadinessProbes[containerCfg.Name] = *containerCfg.ReadinessProbe
				}
			}
			break
		}
	}
}
