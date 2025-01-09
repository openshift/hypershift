package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/support/testutil"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/google/go-cmp/cmp"
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
			nodes:         nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withPairLabel("pair1", 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(10),
		},
		{
			name:          "Hosted cluster has additional node selector (small)",
			hostedCluster: hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=small", hyperv1.NodeSizeLabel)), withHCSizeLabel("small")),
			machineSets:   machineSets(10, withReplicas(1)),
			nodes:         nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withPairLabel("pair1", 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(10),
			expected:      machineSets(6, withReplicas(1))[2:], // machinesets 2, 3, 4, 5
		},
		{
			name:               "Hosted cluster has additional node selector (medium), some nodes are new",
			hostedCluster:      hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=medium", hyperv1.NodeSizeLabel))),
			machineSets:        machineSets(10, withReplicas(1)),
			nodes:              nodes(10, withHC(hc, 0, 1, 2, 3, 4, 5), withPairLabel("pair1", 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5), withCreationTimestamp(twoMinutesAgo, 0, 1)),
			machines:           machines(10),
			expected:           machineSets(6, withReplicas(1))[4:], // machinesets 4, 5
			expectRequeueAfter: nodeScaleDownDelay,
		},
		{
			name:               "Hosted cluster has additional node selector (large), all nodes are new",
			hostedCluster:      hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=large", hyperv1.NodeSizeLabel)), withHCSizeLabel("large")),
			machineSets:        machineSets(4, withReplicas(1)),
			nodes:              nodes(4, withHC(hc, 0, 1, 2, 3), withPairLabel("pair1", 0, 1, 2, 3), withSizeLabel("medium", 0, 1), withSizeLabel("large", 2, 3), withCreationTimestamp(twoMinutesAgo, 0, 1, 2, 3)),
			machines:           machines(10),
			expected:           nil,
			expectRequeueAfter: nodeScaleDownDelay,
		},
		{
			name:          "Hosted cluster has additional node selector (medium) and size label(small)",
			hostedCluster: hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=small", hyperv1.NodeSizeLabel)), withHCSizeLabel("medium")),
			machineSets:   machineSets(6, withReplicas(1)),
			nodes:         nodes(6, withHC(hc, 0, 1, 2, 3, 4, 5), withPairLabel("pair1", 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3), withSizeLabel("large", 4, 5)),
			machines:      machines(6),
			expected:      machineSets(6, withReplicas(1))[4:], // machinesets 4, 5
		},
		{
			name:          "Do not scale down nodes without a size label",
			hostedCluster: hostedCluster(withAdditionalNodeSelector(fmt.Sprintf("%s=small", hyperv1.NodeSizeLabel)), withHCSizeLabel("small")),
			machineSets:   machineSets(6, withReplicas(1)),
			nodes:         nodes(6, withHC(hc, 0, 1, 2, 3, 4, 5), withPairLabel("pair1", 0, 1, 2, 3, 4, 5), withSizeLabel("small", 0, 1), withSizeLabel("medium", 2, 3)),
			machines:      machines(6),
			expected:      machineSets(6, withReplicas(1))[2:4], //machinesets 2, 3
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual, requeueAfter := hostedClusterMachineSetsToScaleDown(context.Background(), test.hostedCluster, test.machineSets, test.machines, test.nodes)
			g.Expect(actual).To(testutil.MatchExpected(test.expected))
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
		{
			name: "Do not scale down nodes without a size label",
			node: &(nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5))[3]), // node 3
			nodes: nodes(8, withHC(hostedCluster(), 0, 1, 2, 3, 4, 5),
				withSizeLabel("small", 0, 1, 6, 7),
				withSizeLabel("medium", 2, 3),
				withSizeLabel("", 4, 5),
				withPairLabel("pair1", 0, 1, 2, 3, 4, 5)),
			machineSets: machineSets(8, withReplicas(1)),
			machines:    machines(8),
			expected:    machineSets(8, withReplicas(1))[:4], // machinesets 0, 1, 2, 3
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := nodeMachineSetsToScaleDown(test.node, test.machineSets, test.machines, test.nodes)
			g.Expect(actual).To(testutil.MatchExpected(test.expected))
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
			pods:  pods(4, pending(0), withPodPairLabel("foo", 0), scheduled(1, 2, 3)),
			nodes: nodes(4),
			expected: []nodeRequirement{
				{
					sizeLabel: "small",
					count:     1,
					pairLabel: "foo",
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
			name:  "ignore unpaired pods without pair selector",
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
			g.Expect(actual).To(testutil.MatchExpected(test.expected, cmp.AllowUnexported(nodeRequirement{})))
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

func TestValidateConfigForNonRequestServing(t *testing.T) {
	validQty := resource.MustParse("0.1")
	tests := []struct {
		name        string
		cfg         *schedulingv1alpha1.ClusterSizingConfiguration
		expectValid bool
	}{
		{
			name:        "Invalid config (no status)",
			cfg:         &schedulingv1alpha1.ClusterSizingConfiguration{},
			expectValid: false,
		},
		{
			name: "Invalid config (valid condition false)",
			cfg: &schedulingv1alpha1.ClusterSizingConfiguration{
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expectValid: false,
		},
		{
			name: "Invalid config (no nodes per zone on all sizes)",
			cfg: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "small",
							Management: &schedulingv1alpha1.Management{
								NonRequestServingNodesPerZone: &validQty,
							},
						},
						{
							Name: "medium",
						},
						{
							Name: "large",
							Management: &schedulingv1alpha1.Management{
								Placeholders: 0,
							},
						},
					},
				},
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectValid: false,
		},
		{
			name: "Valid config",
			cfg: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "small",
							Management: &schedulingv1alpha1.Management{
								NonRequestServingNodesPerZone: &validQty,
							},
						},
						{
							Name: "medium",
							Management: &schedulingv1alpha1.Management{
								NonRequestServingNodesPerZone: &validQty,
							},
						},
						{
							Name: "large",
							Management: &schedulingv1alpha1.Management{
								Placeholders:                  0,
								NonRequestServingNodesPerZone: &validQty,
							},
						},
					},
				},
				Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectValid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := validateConfigForNonRequestServing(test.cfg)
			if test.expectValid {
				g.Expect(actual).To(BeNil())
			} else {
				g.Expect(actual).ToNot(BeNil())
			}
		})
	}
}

func TestValidateNonRequestServingMachineSets(t *testing.T) {

	ms := func(n int, min, max string) machinev1beta1.MachineSet {
		result := machinev1beta1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("ms-%d", n),
			},
		}
		if min != "" || max != "" {
			result.Annotations = map[string]string{}
			if min != "" {
				result.Annotations[minSizeMachineSetAnnotation] = min
			}
			if max != "" {
				result.Annotations[maxSizeMachineSetAnnotation] = max
			}
		}
		return result
	}

	tests := []struct {
		name        string
		machineSets []machinev1beta1.MachineSet
		expectValid bool
	}{
		{
			name:        "Invalid machinesets, not 3",
			machineSets: []machinev1beta1.MachineSet{ms(1, "0", "1"), ms(2, "0", "1")},
			expectValid: false,
		},
		{
			name: "Invalid machinesets, different min/max",
			machineSets: []machinev1beta1.MachineSet{
				ms(1, "0", "1"),
				ms(2, "0", "2"),
				ms(3, "1", "2"),
			},
			expectValid: false,
		},
		{
			name: "Invalid machinesets, no min/max",
			machineSets: []machinev1beta1.MachineSet{
				ms(1, "0", "1"),
				ms(2, "", "1"),
				ms(3, "0", ""),
			},
			expectValid: false,
		},
		{
			name: "Invalid machinesets, invalid min/max",
			machineSets: []machinev1beta1.MachineSet{
				ms(1, "0", "1"),
				ms(2, "1", "0"),
				ms(3, "1", "0"),
			},
			expectValid: false,
		},
		{
			name: "Invalid machinesets, parse error",
			machineSets: []machinev1beta1.MachineSet{
				ms(1, "foo", "3"),
				ms(2, "foo", "3"),
				ms(3, "foo", "3"),
			},
			expectValid: false,
		},
		{
			name: "Valid machinesets",
			machineSets: []machinev1beta1.MachineSet{
				ms(1, "1", "3"),
				ms(2, "1", "3"),
				ms(3, "1", "3"),
			},
			expectValid: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := validateNonRequestServingMachineSets(test.machineSets)
			if test.expectValid {
				g.Expect(actual).To(BeNil())
			} else {
				g.Expect(actual).ToNot(BeNil())
			}
		})
	}
}

func TestNonRequestServingMachineSetsToScale(t *testing.T) {

	ms := func(n int, current int32) machinev1beta1.MachineSet {
		result := machinev1beta1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("ms-%d", n),
				Annotations: map[string]string{
					minSizeMachineSetAnnotation: "1",
					maxSizeMachineSetAnnotation: "10",
				},
			},
			Spec: machinev1beta1.MachineSetSpec{
				Replicas: &current,
			},
		}
		return result
	}

	hc := func(n int, size string) hyperv1.HostedCluster {
		return hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("hc-%d", n),
				Labels: map[string]string{
					hyperv1.HostedClusterSizeLabel: size,
				},
			},
		}
	}
	hcs := func(count int, size string) []hyperv1.HostedCluster {
		result := make([]hyperv1.HostedCluster, 0, count)
		for i := 0; i < count; i++ {
			result = append(result, hc(i, size))
		}
		return result
	}

	smallNodes := resource.MustParse("0.2")  // 5 per node
	mediumNodes := resource.MustParse("0.5") // 2 per node
	largeNodes := resource.MustParse("1")    // 1 per node
	bufferSize := resource.MustParse("1")

	cfg := &schedulingv1alpha1.ClusterSizingConfiguration{
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
			Sizes: []schedulingv1alpha1.SizeConfiguration{
				{
					Name: "small",
					Management: &schedulingv1alpha1.Management{
						NonRequestServingNodesPerZone: &smallNodes,
					},
				},
				{
					Name: "medium",
					Management: &schedulingv1alpha1.Management{
						NonRequestServingNodesPerZone: &mediumNodes,
					},
				},
				{
					Name: "large",
					Management: &schedulingv1alpha1.Management{
						Placeholders:                  0,
						NonRequestServingNodesPerZone: &largeNodes,
					},
				},
			},
			NonRequestServingNodesBufferPerZone: &bufferSize,
		},
		Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	tests := []struct {
		name           string
		hostedClusters []hyperv1.HostedCluster
		machineSets    []machinev1beta1.MachineSet
		expect         []machineSetReplicas
	}{
		{
			name:        "No hosted clusters",
			machineSets: []machinev1beta1.MachineSet{ms(1, 1), ms(2, 1), ms(3, 1)},
			expect:      nil, // no changes
		},
		{
			name:        "No hosted clusters, one machineset scaled up",
			machineSets: []machinev1beta1.MachineSet{ms(1, 1), ms(2, 2), ms(3, 1)},
			expect:      []machineSetReplicas{{ms(2, 2), 1}},
		},
		{
			name:           "Small hosted clusters",
			hostedClusters: []hyperv1.HostedCluster{hc(1, "small"), hc(2, "small"), hc(3, "small")}, // 0.6 should require 1 node + 1 buffer
			machineSets:    []machinev1beta1.MachineSet{ms(1, 1), ms(2, 1), ms(3, 1)},
			expect:         []machineSetReplicas{{ms(1, 1), 2}, {ms(2, 1), 2}, {ms(3, 1), 2}},
		},
		{
			name:           "Small and medium hosted clusters, one machineset scaled up",
			hostedClusters: []hyperv1.HostedCluster{hc(1, "medium"), hc(2, "medium"), hc(3, "small")}, // 1.2 should require 2 nodes + 1 buffer
			machineSets:    []machinev1beta1.MachineSet{ms(1, 1), ms(2, 1), ms(3, 3)},
			expect:         []machineSetReplicas{{ms(1, 1), 3}, {ms(2, 1), 3}},
		},
		{
			name:           "Large hosted clusters, more than max",
			hostedClusters: hcs(20, "large"), // should require 20 nodes + 1 buffer, but we're limited to 10
			machineSets:    []machinev1beta1.MachineSet{ms(1, 1), ms(2, 1), ms(3, 1)},
			expect:         []machineSetReplicas{{ms(1, 1), 10}, {ms(2, 1), 10}, {ms(3, 1), 10}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := nonRequestServingMachineSetsToScale(context.Background(), cfg, test.hostedClusters, test.machineSets)
			g.Expect(actual).To(testutil.MatchExpected(test.expect, cmp.AllowUnexported(machineSetReplicas{})))
		})
	}
}
