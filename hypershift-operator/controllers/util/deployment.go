package util

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DefaultPriorityClass is for pods in the Hypershift control plane that are
	// not API critical but still need elevated priority.
	DefaultPriorityClass = "hypershift-control-plane"
)

func SetDefaultPriorityClass(deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.PriorityClassName = DefaultPriorityClass
}

func SetRestartAnnotation(hc *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
	if value, ok := hc.Annotations[hyperv1.RestartDateAnnotation]; ok {
		if deployment.Spec.Template.ObjectMeta.Annotations == nil {
			deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
		}
		deployment.Spec.Template.ObjectMeta.Annotations[hyperv1.RestartDateAnnotation] = value
	}
}

func SetMultizoneSpread(labels map[string]string, deployment *appsv1.Deployment) {
	if deployment.Spec.Template.Spec.Affinity == nil {
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if deployment.Spec.Template.Spec.Affinity.PodAntiAffinity == nil {
		deployment.Spec.Template.Spec.Affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}
	deployment.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution =
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

func colocationLabel(hc *hyperv1.HostedCluster) string {
	return clusterKey(hc)
}

// SetColocation sets labels and affinity rules for this deployment so that pods
// of the deployment will prefer to group with pods of the anchor deployment as
// established by SetColocationAnchor.
func SetColocation(hc *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
	if deployment.Spec.Template.Spec.Affinity == nil {
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if deployment.Spec.Template.Spec.Affinity.PodAffinity == nil {
		deployment.Spec.Template.Spec.Affinity.PodAffinity = &corev1.PodAffinity{}
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	deployment.Spec.Template.ObjectMeta.Labels[colocationLabelKey] = colocationLabel(hc)
	deployment.Spec.Template.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.WeightedPodAffinityTerm{
		{
			Weight: 100,
			PodAffinityTerm: corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						colocationLabelKey: colocationLabel(hc),
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

func clusterKey(hc *hyperv1.HostedCluster) string {
	return fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
}

func SetControlPlaneIsolation(hc *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Tolerations = []corev1.Toleration{
		{
			Key:      controlPlaneWorkloadTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      clusterWorkloadTolerationKey,
			Operator: corev1.TolerationOpEqual,
			Value:    clusterKey(hc),
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	if deployment.Spec.Template.Spec.Affinity == nil {
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{}
	}
	if deployment.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		deployment.Spec.Template.Spec.Affinity.NodeAffinity = &corev1.NodeAffinity{}
	}
	deployment.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []corev1.PreferredSchedulingTerm{
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
						Values:   []string{clusterKey(hc)},
					},
				},
			},
		},
	}
}
