package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestHostedClusterMachineSetsToScaleDown(t *testing.T) {
	hc := hostedCluster()
	twoMinutesAgo := time.Now().Add(-2 * time.Minute)
	tests := []struct {
		name               string
		hostedCluster      *hyperv1.HostedCluster
		machineSets        []machinev1beta1.MachineSet
		nodes              []corev1.Node
		machines           []machinev1beta1.Machine
		expected           []machinev1beta1.MachineSet
		expectRequeueAfter time.Duration
	}{
		{
			name:          "Hosted cluster has no additional node selector - (migrating from legacy scheduler)",
			hostedCluster: hc,
			machineSets:   machineSets(10),
			nodes:         nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(10),
		},
		{
			name:          "Hosted cluster has additional node selector (small)",
			hostedCluster: hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=small", hyperv1.NodeSizeLabel)), withHCSizeLabel("small")),
			machineSets:   machineSets(10, withReplicas(1)),
			nodes:         nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(10),
			expected:      machineSets(6, withReplicas(1))[2:], // machinesets 2, 3, 4, 5
		},
		{
			name:               "Hosted cluster has additional node selector (medium), some nodes are new",
			hostedCluster:      hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=medium", hyperv1.NodeSizeLabel))),
			machineSets:        machineSets(10, withReplicas(1)),
			nodes:              nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5), withCreationTimestamp(twoMinutesAgo, 0, 1)),
			machines:           machines(10),
			expected:           machineSets(6, withReplicas(1))[4:], // machinesets 4, 5
			expectRequeueAfter: nodeScaleDownDelay,
		},
		{
			name:               "Hosted cluster has additional node selector (large), all nodes are new",
			hostedCluster:      hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=large", hyperv1.NodeSizeLabel)), withHCSizeLabel("large")),
			machineSets:        machineSets(4, withReplicas(1)),
			nodes:              nodes(4, withHC(hc, 0, 1, 2, 3), withSizeLabel("medium", 0, 1), withSizeLabel("large", 2, 3), withCreationTimestamp(twoMinutesAgo, 0, 1, 2, 3)),
			machines:           machines(10),
			expected:           nil,
			expectRequeueAfter: nodeScaleDownDelay,
		},
		{
			name:          "Hosted cluster has additional node selector (medium) and size label(small)",
			hostedCluster: hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=small", hyperv1.NodeSizeLabel)), withHCSizeLabel("medium")),
			machineSets:   machineSets(6, withReplicas(1)),
			nodes:         nodes(6, withHC(hc, 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(6),
			expected:      machineSets(6, withReplicas(1))[4:], // machinesets 4, 5
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual, requeueAfter := hostedClusterMachineSetsToScaleDown(context.Background(), test.hostedCluster, test.machineSets, test.machines, test.nodes)
			g.Expect(actual).To(BeEquivalentTo(test.expected))
			g.Expect(requeueAfter).To(Equal(test.expectRequeueAfter))
		})
	}
}

func TestNodeMachineSetsToScaleDown(t *testing.T) {
	tests := []struct {
		name        string
		node        *corev1.Node
		nodes       []corev1.Node
		machineSets []machinev1beta1.MachineSet
		machines    []machinev1beta1.Machine
		expected    []machinev1beta1.MachineSet
	}{
		{
			name: "There are multiple nodes with the same pair label, machinesets are scaled up",
			node: &(nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5))[3]), // node 3
			nodes: nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5)),
			machineSets: machineSets(8, withReplicas(1)),
			machines:    machines(8),
			expected:    machineSets(8, withReplicas(1))[:6], // machinesets 0, 1, 2, 3, 4, 5
		},
		{
			name: "There are multiple nodes with the same pair label, some machinesets are scaled up",
			node: &(nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5))[3]), // node 3
			nodes: nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5)),
			machineSets: machineSets(8, withReplicas(1, 0, 1, 4, 5)),
			machines:    machines(8),
			expected:    append(machineSets(8, withReplicas(1))[:2], machineSets(8, withReplicas(1))[4:6]...), // machinesets 0, 1, 4, 5
		},
		{
			name: "The node does not have a pair label",
			node: &(nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("", 0, 1, 2, 3, 4, 5))[3]), // node 3
			nodes: nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("large", 4, 5),
				withPairLabel("", 0, 1, 2, 3, 4, 5)),
			machineSets: machineSets(8, withReplicas(1)),
			machines:    machines(8),
			expected:    machineSets(8, withReplicas(1))[3:4], // machineset 3
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := nodeMachineSetsToScaleDown(test.node, test.machineSets, test.machines, test.nodes)
			g.Expect(actual).To(BeEquivalentTo(test.expected))
		})
	}
}

func TestMachineSetsToScaleUp(t *testing.T) {
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
			pods:        pods(10, pending(5, 6), withPodPairLabel("pair-3", 5, 6)),
			machineSets: machineSets(10),
			expected:    []string{"machineset-6", "machineset-7"}, // machinesets 6 and 7 have the matching pair label
		},
	}

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, _ := machineSetsToScaleUp(tt.pods, tt.machineSets, tt.machines, tt.nodes)
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

func scheduled(indices ...int) func([]corev1.Pod) {
	return func(pods []corev1.Pod) {
		for _, i := range indices {
			pods[i].Spec.NodeName = fmt.Sprintf("node-%d", i)
		}
	}
}

func withPodPairLabel(pair string, indices ...int) func([]corev1.Pod) {
	return func(pods []corev1.Pod) {
		for _, i := range indices {
			pods[i].Spec.NodeSelector[OSDFleetManagerPairedNodesLabel] = pair
		}
	}
}

func machines(count int) []machinev1beta1.Machine {
	machines := make([]machinev1beta1.Machine, 0, count)
	for i := 0; i < count; i++ {
		machines = append(machines, machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("machine-%d", i),
				Namespace: "openshift-machine-api",
				Labels: map[string]string{
					"machine.openshift.io/cluster-api-machineset": fmt.Sprintf("machineset-%d", i),
				},
			},
		})
	}
	return machines
}

func machineSets(count int, mods ...func([]machinev1beta1.MachineSet)) []machinev1beta1.MachineSet {
	machineSets := make([]machinev1beta1.MachineSet, 0, count)
	for i := 0; i < count; i++ {
		machineSets = append(machineSets, machinev1beta1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("machineset-%d", i),
				Namespace: "openshift-machine-api",
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
	for _, mod := range mods {
		mod(machineSets)
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
					"machine.openshift.io/machine": fmt.Sprintf("openshift-machine-api/machine-%d", i),
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

func withHC(hc *hyperv1.HostedCluster, indices ...int) func([]corev1.Node) {
	return func(nodes []corev1.Node) {
		for _, i := range indices {
			nodes[i].Labels[hyperv1.HostedClusterLabel] = clusterKey(hc)
			nodes[i].Labels[HostedClusterNamespaceLabel] = hc.Namespace
			nodes[i].Labels[HostedClusterNameLabel] = hc.Name
		}
	}
}

func withSizeLabel(size string, indices ...int) func([]corev1.Node) {
	return func(nodes []corev1.Node) {
		for _, i := range indices {
			nodes[i].Labels[hyperv1.NodeSizeLabel] = size
		}
	}
}

func withPairLabel(pair string, indices ...int) func([]corev1.Node) {
	return func(nodes []corev1.Node) {
		for _, i := range indices {
			nodes[i].Labels[OSDFleetManagerPairedNodesLabel] = pair
		}
	}
}

func withCreationTimestamp(t time.Time, indices ...int) func([]corev1.Node) {
	return func(nodes []corev1.Node) {
		for _, i := range indices {
			nodes[i].CreationTimestamp = metav1.NewTime(t)
		}
	}
}

func withReplicas(replicas int32, indices ...int) func(machineSets []machinev1beta1.MachineSet) {
	return func(machineSets []machinev1beta1.MachineSet) {
		if len(indices) == 0 {
			for i := range machineSets {
				machineSets[i].Spec.Replicas = &replicas
			}
			return
		}
		for _, i := range indices {
			machineSets[i].Spec.Replicas = &replicas
		}
	}
}

func TestDetermineRequiredNodes(t *testing.T) {
	tests := []struct {
		name     string
		pods     []corev1.Pod
		nodes    []corev1.Node
		expected []nodeRequirement
	}{
		{
			name:     "No pending pods",
			pods:     pods(4, scheduled(0, 1, 2, 3)),
			nodes:    nodes(4),
			expected: nil,
		},
		{
			name:  "Paired pending pods",
			pods:  pods(8, pending(0, 1, 2, 3), scheduled(4, 5, 6, 7)),
			nodes: nodes(8),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     4,
				},
			},
		},
		{
			name:  "Single pending pod",
			pods:  pods(4, pending(0), scheduled(1, 2, 3)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     1,
					pairLabel: "pair-0",
				},
			},
		},
		{
			name:  "Single pending pod, with pending/scheduled pair",
			pods:  pods(4, pending(0, 1), scheduled(0, 2, 3)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     2,
					pairLabel: "pair-0",
				},
			},
		},
		{
			name:  "Pods of different pairs pending",
			pods:  pods(4, pending(0, 1, 2, 3), scheduled(1, 2)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     2,
					pairLabel: "pair-0",
				},
				{
					sizeLabel: "small",
					count:     2,
					pairLabel: "pair-1",
				},
			},
		},
		{
			name:  "Pods of different pairs pending, along with unpaired",
			pods:  pods(6, pending(0, 1, 2, 3, 4, 5), scheduled(1, 2)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     2,
					pairLabel: "pair-0",
				},
				{
					sizeLabel: "small",
					count:     2,
					pairLabel: "pair-1",
				},
				{
					sizeLabel: "small",
					count:     2,
				},
			},
		},
		{
			name:  "ignore unpaired pods",
			pods:  pods(3, pending(0, 1, 2)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     2,
				},
			},
		},
	}
	pendingPods := func(pods []corev1.Pod) []corev1.Pod {
		result := make([]corev1.Pod, 0, len(pods))
		for _, pod := range pods {
			if pod.Status.Phase == corev1.PodPending {
				result = append(result, pod)
			}
		}
		return result
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := determineRequiredNodes(pendingPods(test.pods), test.pods, test.nodes)
			g.Expect(actual).To(BeEquivalentTo(test.expected))
		})
	}
}

func hostedCluster(mods ...func(*hyperv1.HostedCluster)) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hc-name",
			Namespace: "hc-namespace",
		},
	}
	for _, mod := range mods {
		mod(hc)
	}
	return hc
}

func withAdditionalNodeSelector(selector string) func(*hyperv1.HostedCluster) {
	return func(hc *hyperv1.HostedCluster) {
		if hc.Annotations == nil {
			hc.Annotations = make(map[string]string)
		}
		hc.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = selector
	}
}

func withHCSizeLabel(size string) func(*hyperv1.HostedCluster) {
	return func(hc *hyperv1.HostedCluster) {
		if hc.Labels == nil {
			hc.Labels = make(map[string]string)
		}
		hc.Labels[hyperv1.HostedClusterSizeLabel] = size
	}
}
