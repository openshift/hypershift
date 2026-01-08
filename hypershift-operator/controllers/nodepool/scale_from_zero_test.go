package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

func TestTaintsToAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		taints   []hyperv1.Taint
		expected string
	}{
		{
			name:     "When taints are empty it should return empty string",
			taints:   []hyperv1.Taint{},
			expected: "",
		},
		{
			name: "When single taint it should format correctly",
			taints: []hyperv1.Taint{
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "dedicated=gpu:NoSchedule",
		},
		{
			name: "When single taint with empty value it should format as key:Effect",
			taints: []hyperv1.Taint{
				{Key: "node-role.kubernetes.io/infra", Value: "", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "node-role.kubernetes.io/infra:NoSchedule",
		},
		{
			name: "When multiple taints it should format and sort",
			taints: []hyperv1.Taint{
				{Key: "critical", Value: "true", Effect: corev1.TaintEffectNoExecute},
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "critical=true:NoExecute,dedicated=gpu:NoSchedule",
		},
		{
			name: "When taints with different effects it should format correctly",
			taints: []hyperv1.Taint{
				{Key: "node-role.kubernetes.io/infra", Value: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "workload", Value: "batch", Effect: corev1.TaintEffectPreferNoSchedule},
			},
			expected: "node-role.kubernetes.io/infra:NoSchedule,workload=batch:PreferNoSchedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := taintsToAnnotation(tt.taints)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
