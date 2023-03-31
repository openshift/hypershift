package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestSetMachineSetReplicas(t *testing.T) {
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineSet                  *capiv1.MachineSet
		expectReplicas              int32
		expectAutoscalerAnnotations map[string]string
	}{
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineSet has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 1,
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it does not set current replicas but set annotations when autoscaling is enabled" +
				" and the MachineSet has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.min and set annotations when autoscaling is enabled" +
				" and the MachineSet has replicas < autoScaling.min",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: k8sutilspointer.Int32Ptr(1),
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.max and set annotations when autoscaling is enabled" +
				" and the MachineSet has replicas > autoScaling.max",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: 2,
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: k8sutilspointer.Int32Ptr(10),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineSetReplicas(tc.nodePool, tc.machineSet)
			g.Expect(*tc.machineSet.Spec.Replicas).To(Equal(tc.expectReplicas))
			g.Expect(tc.machineSet.Annotations).To(Equal(tc.expectAutoscalerAnnotations))
		})
	}
}
