package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func kubevirtMachineTemplateSpec(nodePool *hyperv1.NodePool) *capikubevirt.KubevirtMachineTemplateSpec {
	nodePoolNameLabelKey := "hypershift.kubevirt.io/node-pool-name"

	vmTemplate := nodePool.Spec.Platform.Kubevirt.NodeTemplate

	if vmTemplate.Spec.Template.Spec.Affinity == nil {
		vmTemplate.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
					{
						Weight: int32(100),
						PodAffinityTerm: corev1.PodAffinityTerm{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      nodePoolNameLabelKey,
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{nodePool.Name},
									},
								},
							},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			},
		}
	}

	if vmTemplate.ObjectMeta.Labels == nil {
		vmTemplate.ObjectMeta.Labels = map[string]string{}
	}
	if vmTemplate.Spec.Template.ObjectMeta.Labels == nil {
		vmTemplate.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}

	vmTemplate.Spec.Template.ObjectMeta.Labels[nodePoolNameLabelKey] = nodePool.Name
	vmTemplate.ObjectMeta.Labels[nodePoolNameLabelKey] = nodePool.Name

	return &capikubevirt.KubevirtMachineTemplateSpec{
		Template: capikubevirt.KubevirtMachineTemplateResource{
			Spec: capikubevirt.KubevirtMachineSpec{
				VirtualMachineTemplate: *vmTemplate,
			},
		},
	}
}
