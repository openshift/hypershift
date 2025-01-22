package inplaceupgrader

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetNodesForMachineSet(t *testing.T) {
	_ = capiv1.AddToScheme(scheme.Scheme)

	selector := map[string]string{
		"foo": "bar",
	}

	machineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "valid",
		},
		Spec: capiv1.MachineSetSpec{
			Selector: metav1.LabelSelector{
				MatchLabels:      selector,
				MatchExpressions: nil,
			},
		},
	}

	machines := []client.Object{
		&capiv1.Machine{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "MachineSet",
						Name:       "test",
						Controller: ptr.To(true),
						UID:        "valid",
					},
				},
				Name:   "test",
				Labels: selector,
			},
			Status: capiv1.MachineStatus{
				NodeRef: &corev1.ObjectReference{
					Name: "test",
				},
			},
		},
		&capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "other",
						Name:       "test",
						Controller: ptr.To(true),
						UID:        "other",
					},
				},
				Name:   "otherOwner",
				Labels: selector,
			},
			Status: capiv1.MachineStatus{
				NodeRef: &corev1.ObjectReference{
					Name: "otherOwner",
				},
			},
		},
		&capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "MachineSet",
						Name:       "test",
						Controller: ptr.To(true),
						UID:        "valid",
					},
				},
				Name: "otherSelector",
				Labels: map[string]string{
					"other": "",
				},
			},
			Status: capiv1.MachineStatus{
				NodeRef: &corev1.ObjectReference{
					Name: "otherSelector",
				},
			},
		},
	}

	wantedNodes := []client.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
		},
	}

	unwantedNodes := []client.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "otherSelector",
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "otherOwner",
			},
		},
	}

	c := fake.NewClientBuilder().WithObjects(machineSet).WithObjects(machines...).Build()
	hostedClusterClient := fake.NewClientBuilder().WithObjects(wantedNodes...).WithObjects(unwantedNodes...).Build()
	gotNodes, err := getNodesForMachineSet(context.Background(), c, hostedClusterClient, machineSet)

	g := NewWithT(t)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(gotNodes)).To(BeIdenticalTo(len(wantedNodes)))
	for i := range wantedNodes {
		found := false
		for j := range gotNodes {
			if wantedNodes[i].GetName() == gotNodes[j].Name {
				found = true
				break
			}
			g.Expect(found).To(BeTrue())
		}
	}
}

func TestInPlaceUpgradeComplete(t *testing.T) {
	var currentConfigHash = "aaa"
	var desiredConfigHash = "bbb"
	var drainRequest = "drain-xxx"
	var uncordonRequest = "uncordon-xxx"
	testCases := []struct {
		name          string
		currentConfig string
		desiredConfig string
		nodes         []*corev1.Node
		complete      bool
	}{
		{
			name:          "freshly installed nodepool",
			currentConfig: currentConfigHash,
			desiredConfig: currentConfigHash,
			nodes: []*corev1.Node{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "node1",
						Annotations: map[string]string{},
					},
				},
			},
			complete: true,
		},
		{
			name:          "update starting",
			currentConfig: currentConfigHash,
			desiredConfig: desiredConfigHash,
			nodes: []*corev1.Node{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "node1",
						Annotations: map[string]string{},
					},
				},
			},
			complete: false,
		},
		{
			name:          "update in progress",
			currentConfig: currentConfigHash,
			desiredConfig: desiredConfigHash,
			nodes: []*corev1.Node{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "node1",
						Annotations: map[string]string{},
					},
				},
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node2",
						Annotations: map[string]string{
							DesiredMachineConfigAnnotationKey: desiredConfigHash,
						},
					},
				},
			},
			complete: false,
		},
		{
			name:          "update completed but uncordon not yet finished",
			currentConfig: currentConfigHash,
			desiredConfig: desiredConfigHash,
			nodes: []*corev1.Node{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node1",
						Annotations: map[string]string{
							CurrentMachineConfigAnnotationKey: desiredConfigHash,
							DesiredMachineConfigAnnotationKey: desiredConfigHash,
						},
					},
				},
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node2",
						Annotations: map[string]string{
							CurrentMachineConfigAnnotationKey: desiredConfigHash,
							DesiredMachineConfigAnnotationKey: desiredConfigHash,
							DesiredDrainerAnnotationKey:       drainRequest,
							LastAppliedDrainerAnnotationKey:   uncordonRequest,
						},
					},
				},
			},
			complete: false,
		},
		{
			name:          "fully completed update",
			currentConfig: currentConfigHash,
			desiredConfig: desiredConfigHash,
			nodes: []*corev1.Node{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node1",
						Annotations: map[string]string{
							CurrentMachineConfigAnnotationKey:     desiredConfigHash,
							DesiredMachineConfigAnnotationKey:     desiredConfigHash,
							DesiredDrainerAnnotationKey:           uncordonRequest,
							LastAppliedDrainerAnnotationKey:       uncordonRequest,
							MachineConfigDaemonStateAnnotationKey: MachineConfigDaemonStateDone,
						},
					},
				},
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node2",
						Annotations: map[string]string{
							CurrentMachineConfigAnnotationKey:     desiredConfigHash,
							DesiredMachineConfigAnnotationKey:     desiredConfigHash,
							DesiredDrainerAnnotationKey:           uncordonRequest,
							LastAppliedDrainerAnnotationKey:       uncordonRequest,
							MachineConfigDaemonStateAnnotationKey: MachineConfigDaemonStateDone,
						},
					},
				},
			},
			complete: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(inPlaceUpgradeComplete(tc.nodes, tc.currentConfig, tc.desiredConfig)).To(Equal(tc.complete))
		})
	}
}

func TestGetNodesToUpgrade(t *testing.T) {
	currentConfigHash := "aaa"
	intermediateConfigHash := "bbb"
	desiredConfigHash := "ccc"
	awaitingNode1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node1",
			Annotations: map[string]string{},
		},
	}
	awaitingNode2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node2",
			Annotations: map[string]string{},
		},
	}
	inProgressNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node3",
			Annotations: map[string]string{
				DesiredMachineConfigAnnotationKey: desiredConfigHash,
			},
		},
	}
	completedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node4",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: desiredConfigHash,
				DesiredMachineConfigAnnotationKey: desiredConfigHash,
			},
		},
	}
	intermediateNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node5",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: currentConfigHash,
				DesiredMachineConfigAnnotationKey: intermediateConfigHash,
			},
		},
	}
	testCases := []struct {
		name           string
		inputNodes     []*corev1.Node
		targetConfig   string
		maxUnavailable int
		selectedNodes  []*corev1.Node
	}{
		{
			name: "pick first node to upgrade",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 1,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
			},
		},
		{
			name: "select multiple nodes to upgrade",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 2,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
		},
		{
			name: "maxUnavailable reached",
			inputNodes: []*corev1.Node{
				inProgressNode,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 1,
			selectedNodes:  nil,
		},
		{
			name: "all nodes comeplete",
			inputNodes: []*corev1.Node{
				completedNode,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 1,
			selectedNodes:  nil,
		},
		{
			name: "pick 1 ready node if possible",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				inProgressNode,
				completedNode,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 2,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
			},
		},
		{
			name: "pick correct nodes to upgrade",
			inputNodes: []*corev1.Node{
				inProgressNode,
				completedNode,
				awaitingNode1,
				awaitingNode2,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 3,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
		},
		// This test case covers a scenario where a new update comes in while an update is in progress
		{
			name: "points in progress nodes to latest version",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				completedNode,
				intermediateNode,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 1,
			selectedNodes: []*corev1.Node{
				intermediateNode,
			},
		},
		{
			name: "points in progress nodes to latest version, while also picking based on maxUnavailable",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				completedNode,
				intermediateNode,
			},
			targetConfig:   desiredConfigHash,
			maxUnavailable: 2,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
				intermediateNode,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(getNodesToUpgrade(tc.inputNodes, tc.targetConfig, tc.maxUnavailable)).To(Equal(tc.selectedNodes))
		})
	}
}

func TestGetAvailableCandidates(t *testing.T) {
	desiredConfigHash := "ccc"
	awaitingNode1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node1",
			Annotations: map[string]string{},
		},
	}
	awaitingNode2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node2",
			Annotations: map[string]string{},
		},
	}
	inProgressNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node3",
			Annotations: map[string]string{
				DesiredMachineConfigAnnotationKey: desiredConfigHash,
			},
		},
	}
	completedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node4",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: desiredConfigHash,
				DesiredMachineConfigAnnotationKey: desiredConfigHash,
			},
		},
	}
	testCases := []struct {
		name          string
		inputNodes    []*corev1.Node
		targetConfig  string
		capacity      int
		selectedNodes []*corev1.Node
	}{
		{
			name: "pick first node to upgrade",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
			targetConfig: desiredConfigHash,
			capacity:     1,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
			},
		},
		{
			name: "select non-completed node",
			inputNodes: []*corev1.Node{
				completedNode,
				awaitingNode1,
			},
			targetConfig: desiredConfigHash,
			capacity:     1,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
			},
		},
		{
			name: "pick more nodes up to capacity",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
				inProgressNode,
				completedNode,
			},
			targetConfig: desiredConfigHash,
			capacity:     2,
			selectedNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
			},
		},
		{
			name: "do nothing while no additional capacity",
			inputNodes: []*corev1.Node{
				awaitingNode1,
				awaitingNode2,
				completedNode,
			},
			targetConfig:  desiredConfigHash,
			capacity:      0,
			selectedNodes: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(getAvailableCandidates(tc.inputNodes, tc.targetConfig, tc.capacity)).To(Equal(tc.selectedNodes))
		})
	}
}
