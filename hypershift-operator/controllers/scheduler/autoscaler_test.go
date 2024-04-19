package scheduler

import (
	"fmt"
	"testing"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestDetermineMachineSetsToScale(t *testing.T) {
	// Test cases
	tests := []struct {
		name        string
		pods        []corev1.Pod
		machines    []machinev1beta1.Machine
		machineSets []machinev1beta1.MachineSet
		nodes       []corev1.Node
		expected    []string
	}{
		{
			name:        "No pending pods",
			pods:        pods(10),
			machines:    machines(4),
			machineSets: machineSets(4),
			nodes:       nodes(4),
			expected:    []string{},
		},
		{
			name:        "Pending placeholder pods, available machinesets",
			pods:        pods(10, pending(5, 6)),
			machines:    machines(4),
			machineSets: machineSets(8),
			nodes:       nodes(4),
			expected:    []string{"machineset-4", "machineset-5"}, // 4 and 5 are the next available machinesets
		},
		{
			name:        "Pending pods with pair label",
			pods:        pods(10, pending(5, 6), podPair("pair-3", 5, 6)),
			machineSets: machineSets(10),
			expected:    []string{"machineset-6", "machineset-7"}, // machinesets 6 and 7 have the matching pair label
		},
	}

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineMachineSetsToScale(tt.pods, tt.machineSets, tt.machines, tt.nodes)
			expectedSet := sets.New(tt.expected...)
			actualSet := sets.New[string]()
			for _, machineSet := range result {
				actualSet.Insert(machineSet.Name)
			}
			if !expectedSet.Equal(actualSet) {
				t.Errorf("Expected %v, got %v", expectedSet, actualSet)
			}
		})
	}
}

func pods(count int, mods ...func([]corev1.Pod)) []corev1.Pod {
	pods := make([]corev1.Pod, 0, count)
	for i := 0; i < count; i++ {
		pods = append(pods, corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("pod-%d", i),
				Labels: map[string]string{
					"pair": fmt.Sprintf("%d", i/2),
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					hyperv1.NodeSizeLabel: "small",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		})
	}
	for _, mod := range mods {
		mod(pods)
	}
	return pods
}

func pending(indices ...int) func([]corev1.Pod) {
	return func(pods []corev1.Pod) {
		for _, i := range indices {
			pods[i].Status.Phase = corev1.PodPending
		}
	}
}

func podPair(pair string, indices ...int) func([]corev1.Pod) {
	return func(pods []corev1.Pod) {
		for _, i := range indices {
			pods[i].Spec.NodeSelector[OSDFleetManagerPairedNodesLabel] = pair
		}
	}
}

func machines(count int) []machinev1beta1.Machine {
	machines := make([]machinev1beta1.Machine, 0, count)
	for i := 0; i < count; i++ {
		pair := i / 2
		machines = append(machines, machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("machine-%d", i),
				Annotations: map[string]string{
					"machine.openshift.io/cluster-api-machineset": fmt.Sprintf("machineset-%d", pair),
				},
			},
		})
	}
	return machines
}

func machineSets(count int) []machinev1beta1.MachineSet {
	machineSets := make([]machinev1beta1.MachineSet, 0, count)
	for i := 0; i < count; i++ {
		machineSets = append(machineSets, machinev1beta1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("machineset-%d", i),
			},
			Spec: machinev1beta1.MachineSetSpec{
				Template: machinev1beta1.MachineTemplateSpec{
					Spec: machinev1beta1.MachineSpec{
						ObjectMeta: machinev1beta1.ObjectMeta{
							Labels: map[string]string{
								hyperv1.NodeSizeLabel:                "small",
								OSDFleetManagerPairedNodesLabel:      fmt.Sprintf("pair-%d", i/2),
								hyperv1.RequestServingComponentLabel: "true",
							},
						},
					},
				},
			},
		})
	}
	return machineSets
}

func nodes(count int, mods ...func([]corev1.Node)) []corev1.Node {
	nodes := make([]corev1.Node, 0, count)
	for i := 0; i < count; i++ {
		pair := i / 2
		nodes = append(nodes, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Annotations: map[string]string{
					"machine.openshift.io/machine": fmt.Sprintf("machine-%d", i),
				},
				Labels: map[string]string{
					hyperv1.RequestServingComponentLabel: "true",
					hyperv1.NodeSizeLabel:                "small",
					OSDFleetManagerPairedNodesLabel:      fmt.Sprintf("pair-%d", pair),
					hyperv1.HostedClusterLabel:           "hc-name",
				},
			},
		})
	}
	for _, mod := range mods {
		mod(nodes)
	}
	return nodes
}
