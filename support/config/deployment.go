package config

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

type DeploymentConfig struct {
	Replicas                  int                   `json:"replicas"`
	Scheduling                Scheduling            `json:"scheduling"`
	AdditionalLabels          AdditionalLabels      `json:"additionalLabels"`
	AdditionalAnnotations     AdditionalAnnotations `json:"additionalAnnotations"`
	SecurityContexts          SecurityContextSpec   `json:"securityContexts"`
	SetDefaultSecurityContext bool                  `json:"setDefaultSecurityContext"`
	LivenessProbes            LivenessProbes        `json:"livenessProbes"`
	ReadinessProbes           ReadinessProbes       `json:"readinessProbes"`
	Resources                 ResourcesSpec         `json:"resources"`
}

func (c *DeploymentConfig) SetContainerResourcesIfPresent(container *corev1.Container) {
	resources := container.Resources
	if len(resources.Requests) > 0 || len(resources.Limits) > 0 {
		if c.Resources != nil {
			c.Resources[container.Name] = resources
		}
	}
}

func (c *DeploymentConfig) SetRestartAnnotation(objectMetadata metav1.ObjectMeta) {
	if _, ok := objectMetadata.Annotations[hyperv1.RestartDateAnnotation]; ok {
		if c.AdditionalAnnotations == nil {
			c.AdditionalAnnotations = make(AdditionalAnnotations)
		}
		c.AdditionalAnnotations[hyperv1.RestartDateAnnotation] = objectMetadata.Annotations[hyperv1.RestartDateAnnotation]
	}
}

func (c *DeploymentConfig) SetReleaseImageAnnotation(releaseImage string) {
	if c.AdditionalAnnotations == nil {
		c.AdditionalAnnotations = make(AdditionalAnnotations)
	}
	c.AdditionalAnnotations[hyperv1.ReleaseImageAnnotation] = releaseImage
}

func (c *DeploymentConfig) SetMultizoneSpread(labels map[string]string) {
	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAntiAffinity == nil {
		c.Scheduling.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	c.Scheduling.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution =
		[]corev1.PodAffinityTerm{
			{
				TopologyKey: corev1.LabelTopologyZone,
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
			},
		}
}

const colocationLabelKey = "hypershift.openshift.io/hosted-control-plane"

func colocationLabel(hcp *hyperv1.HostedControlPlane) string {
	return clusterKey(hcp)
}

// SetColocationAnchor sets labels on the deployment to establish pods of this
// deployment as an anchor for other pods associated with hcp using pod affinity.
func (c *DeploymentConfig) SetColocationAnchor(hcp *hyperv1.HostedControlPlane) {
	if c.AdditionalLabels == nil {
		c.AdditionalLabels = map[string]string{}
	}
	c.AdditionalLabels[colocationLabelKey] = colocationLabel(hcp)
}

// SetColocation sets labels and affinity rules for this deployment so that pods
// of the deployment will prefer to group with pods of the anchor deployment as
// established by SetColocationAnchor.
func (c *DeploymentConfig) SetColocation(hcp *hyperv1.HostedControlPlane) {
	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.PodAffinity == nil {
		c.Scheduling.Affinity.PodAffinity = &corev1.PodAffinity{}
	}
	if c.AdditionalLabels == nil {
		c.AdditionalLabels = map[string]string{}
	}
	c.AdditionalLabels[colocationLabelKey] = colocationLabel(hcp)
	c.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
		{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						colocationLabelKey: colocationLabel(hcp),
					},
				},
				TopologyKey: corev1.LabelHostname,
			},
		},
	}
}

const (
	controlPlaneWorkloadTolerationKey = "hypershift.openshift.io/control-plane"
	controlPlaneNodeLabel             = "hypershift.openshift.io/control-plane"

	clusterWorkloadTolerationKey = "hypershift.openshift.io/cluster"
	clusterNodeLabel             = "hypershift.openshift.io/cluster"

	// cluster-specific weight for soft affinity rule to node
	clusterNodeSchedulingAffinityWeight = 100

	// generic control plane workload weight for soft affinity rule to node
	controlPlaneNodeSchedulingAffinityWeight = clusterNodeSchedulingAffinityWeight / 2
)

func clusterKey(hcp *hyperv1.HostedControlPlane) string {
	return hcp.Namespace
}

func (c *DeploymentConfig) SetControlPlaneIsolation(hcp *hyperv1.HostedControlPlane) {
	c.Scheduling.Tolerations = []corev1.Toleration{
		{
			Key:      controlPlaneWorkloadTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      clusterWorkloadTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    clusterKey(hcp),
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	if c.Scheduling.Affinity == nil {
		c.Scheduling.Affinity = &corev1.Affinity{}
	}
	if c.Scheduling.Affinity.NodeAffinity == nil {
		c.Scheduling.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	c.Scheduling.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.PreferredSchedulingTerm{
		{
			Weight: controlPlaneNodeSchedulingAffinityWeight,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      controlPlaneNodeLabel,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
			},
		},
		{
			Weight: clusterNodeSchedulingAffinityWeight,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      clusterNodeLabel,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{clusterKey(hcp)},
					},
				},
			},
		},
	}
}

func (c *DeploymentConfig) ApplyTo(deployment *appsv1.Deployment) {
	deployment.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	// there are two standard cases currently with hypershift: HA mode where there are 3 replicas spread across
	// zones and then non ha with one replica. When only 3 zones are available you need to be able to set maxUnavailable
	// in order to progress the rollout. However, you do not want to set that in the single replica case because it will
	// result in downtime.
	if c.Replicas > 1 {
		maxSurge := intstr.FromInt(3)
		maxUnavailable := intstr.FromInt(1)
		if deployment.Spec.Strategy.RollingUpdate == nil {
			deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
		}
		deployment.Spec.Strategy.RollingUpdate.MaxSurge = &maxSurge
		deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	}

	// set default security context for pod
	if c.SetDefaultSecurityContext {
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: pointer.Int64(DefaultSecurityContextUser),
		}
	}

	c.Scheduling.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&deployment.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&deployment.Spec.Template.Spec)
	c.Resources.ApplyTo(&deployment.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&deployment.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToDaemonSet(daemonset *appsv1.DaemonSet) {
	// replicas is not used for DaemonSets
	c.Scheduling.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&daemonset.Spec.Template.Spec)
	c.Resources.ApplyTo(&daemonset.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&daemonset.Spec.Template.ObjectMeta)
}

func (c *DeploymentConfig) ApplyToStatefulSet(sts *appsv1.StatefulSet) {
	sts.Spec.Replicas = pointer.Int32Ptr(int32(c.Replicas))
	c.Scheduling.ApplyTo(&sts.Spec.Template.Spec)
	c.AdditionalLabels.ApplyTo(&sts.Spec.Template.ObjectMeta)
	c.SecurityContexts.ApplyTo(&sts.Spec.Template.Spec)
	c.Resources.ApplyTo(&sts.Spec.Template.Spec)
	c.LivenessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.ReadinessProbes.ApplyTo(&sts.Spec.Template.Spec)
	c.Resources.ApplyTo(&sts.Spec.Template.Spec)
	c.AdditionalAnnotations.ApplyTo(&sts.Spec.Template.ObjectMeta)
}
