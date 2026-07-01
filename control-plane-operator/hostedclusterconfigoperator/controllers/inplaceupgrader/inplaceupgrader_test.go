package inplaceupgrader

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
				NodeRef: capiv1.MachineNodeReference{
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
				NodeRef: capiv1.MachineNodeReference{
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
				NodeRef: capiv1.MachineNodeReference{
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
	gotNodes, nodeToMachine, err := getNodesForMachineSet(t.Context(), c, hostedClusterClient, machineSet)

	g := NewWithT(t)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(gotNodes)).To(BeIdenticalTo(len(wantedNodes)))
	g.Expect(nodeToMachine).To(HaveLen(1))
	g.Expect(nodeToMachine).To(HaveKey("test"))
	g.Expect(nodeToMachine["test"].Name).To(Equal("test"))
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

func TestCreateUpgradePod(t *testing.T) {
	testCases := []struct {
		name         string
		node         *corev1.Node
		proxy        *configv1.Proxy
		expectedEnvs []corev1.EnvVar
	}{
		{
			name: "when proxy is configured, it should create a pod with proxy environment variables",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			},
			proxy: &configv1.Proxy{
				Status: configv1.ProxyStatus{
					HTTPProxy:  "http://proxy.example.com:8080",
					HTTPSProxy: "https://proxy.example.com:8443",
					NoProxy:    "localhost,127.0.0.1",
				},
			},
			expectedEnvs: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://proxy.example.com:8080"},
				{Name: "HTTPS_PROXY", Value: "https://proxy.example.com:8443"},
				{Name: "NO_PROXY", Value: "localhost,127.0.0.1"},
			},
		},
		{
			name: "when no proxy is configured it should create a pod without proxy environment variables",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &Reconciler{}
			pod := inPlaceUpgradePod("ns", "name")
			err := r.createUpgradePod(pod, "nodeName", "poolName", "image", tc.proxy)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(len(pod.Spec.Containers)).To(BeNumerically(">", 0))
			g.Expect(pod.Spec.Containers[0].Image).To(Equal("image"))
			g.Expect(pod.Spec.Containers[0].Args).To(ContainElement("--node-name=nodeName"))

			gotEnvVars := pod.Spec.Containers[0].Env
			g.Expect(len(gotEnvVars)).To(Equal(len(tc.expectedEnvs)), "Expected %d environment variables, got %d", len(tc.expectedEnvs), len(gotEnvVars))
			for _, expectedEnv := range gotEnvVars {
				g.Expect(tc.expectedEnvs).To(ContainElement(expectedEnv), "Expected environment variable %s not found", expectedEnv.Name)
			}
		})
	}
}

func TestReconcileUpgradePods(t *testing.T) {
	_ = capiv1.AddToScheme(scheme.Scheme)

	poolName := "test-pool"
	mcoImage := "mco-image:latest"
	namespace := inPlaceUpgradeNamespace(poolName)
	targetConfig := "target-hash"

	testCases := []struct {
		name              string
		node              *corev1.Node
		existingPod       *corev1.Pod
		expectPodDeleted  bool
		expectPodCreated  bool
		expectPodRetained bool
		expectPodSkipped  bool
	}{
		{
			name: "When MCD pod is in Succeeded phase on a node needing upgrade it should delete the terminated pod",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
				},
			},
			expectPodDeleted: true,
		},
		{
			name: "When MCD pod is in Failed phase on a node needing upgrade it should delete the terminated pod",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			expectPodDeleted: true,
		},
		{
			name: "When MCD pod is in Running phase on a node needing upgrade it should leave the pod alone",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
			expectPodRetained: true,
		},
		{
			name: "When no MCD pod exists on a node needing upgrade it should create a new pod",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod:      nil,
			expectPodCreated: true,
		},
		{
			name: "When terminated MCD pod is already being deleted it should skip without error",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         namespace.Name,
					Name:              "machine-config-daemon-node1",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			expectPodSkipped: true,
		},
		{
			name: "When MCD pod is in Succeeded phase on a fully updated node it should delete the idle pod",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey:     targetConfig,
						DesiredMachineConfigAnnotationKey:     targetConfig,
						DesiredDrainerAnnotationKey:           "uncordon-xxx",
						LastAppliedDrainerAnnotationKey:       "uncordon-xxx",
						MachineConfigDaemonStateAnnotationKey: MachineConfigDaemonStateDone,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
				},
			},
			expectPodDeleted: true,
		},
		{
			name: "When MCD pod is in Pending phase on a node needing upgrade it should leave the pod alone",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey: "old-hash",
						DesiredMachineConfigAnnotationKey: targetConfig,
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
				},
			},
			expectPodRetained: true,
		},
		{
			name: "When node config matches but MCD state is not Done it should delete the terminated pod for retry",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node1",
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey:     targetConfig,
						DesiredMachineConfigAnnotationKey:     targetConfig,
						DesiredDrainerAnnotationKey:           "uncordon-xxx",
						LastAppliedDrainerAnnotationKey:       "uncordon-xxx",
						MachineConfigDaemonStateAnnotationKey: "Working",
					},
				},
			},
			existingPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      "machine-config-daemon-node1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodFailed,
				},
			},
			expectPodDeleted: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var guestObjects []client.Object
			if tc.existingPod != nil {
				guestObjects = append(guestObjects, tc.existingPod)
			}
			guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(guestObjects...).Build()

			r := &Reconciler{
				CreateOrUpdateProvider: upsert.New(false),
			}

			err := r.reconcileUpgradePods(t.Context(), guestClient, []*corev1.Node{tc.node}, poolName, mcoImage, nil)
			g.Expect(err).ToNot(HaveOccurred())

			pod := inPlaceUpgradePod(namespace.Name, tc.node.Name)
			getErr := guestClient.Get(t.Context(), client.ObjectKeyFromObject(pod), pod)

			if tc.expectPodDeleted {
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "expected pod to be deleted, got: %v", getErr)
			}
			if tc.expectPodCreated {
				g.Expect(getErr).ToNot(HaveOccurred(), "expected pod to be created")
				g.Expect(pod.Spec.Containers).To(HaveLen(1))
				g.Expect(pod.Spec.Containers[0].Image).To(Equal(mcoImage))
			}
			if tc.expectPodRetained {
				g.Expect(getErr).ToNot(HaveOccurred(), "expected pod to be retained")
				g.Expect(pod.Status.Phase).To(Equal(tc.existingPod.Status.Phase))
			}
			if tc.expectPodSkipped {
				g.Expect(getErr).ToNot(HaveOccurred(), "expected pod to still exist (skip due to DeletionTimestamp)")
				g.Expect(pod.DeletionTimestamp).ToNot(BeNil(), "expected DeletionTimestamp to remain from original object")
			}
		})
	}
}

func TestReconcileInPlaceUpgradeAnnotatesMachineWithNodePoolVersion(t *testing.T) {
	g := NewWithT(t)
	_ = capiv1.AddToScheme(scheme.Scheme)

	targetConfigVersion := "target-hash"
	currentConfigVersion := "current-hash"
	nodePoolVersion := "4.18.12"

	selector := map[string]string{"pool": "test"}

	machineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ms",
			Namespace: "test-ns",
			UID:       "ms-uid",
			Annotations: map[string]string{
				nodePoolAnnotationTargetConfigVersion:  targetConfigVersion,
				nodePoolAnnotationCurrentConfigVersion: currentConfigVersion,
			},
		},
		Spec: capiv1.MachineSetSpec{
			Selector: metav1.LabelSelector{MatchLabels: selector},
		},
	}

	// Machine owned by the MachineSet, with a node that has completed the upgrade.
	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "test-ns",
			Labels:    selector,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "MachineSet",
					Name:       machineSet.Name,
					UID:        machineSet.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Status: capiv1.MachineStatus{
			NodeRef: capiv1.MachineNodeReference{Name: "test-node"},
		},
	}

	// A second machine+node still upgrading, so inPlaceUpgradeComplete returns false.
	upgradingMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "upgrading-machine",
			Namespace: "test-ns",
			Labels:    selector,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "MachineSet",
					Name:       machineSet.Name,
					UID:        machineSet.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Status: capiv1.MachineStatus{
			NodeRef: capiv1.MachineNodeReference{Name: "upgrading-node"},
		},
	}

	// Node that has completed the upgrade (current == desired == target).
	completedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey:     targetConfigVersion,
				DesiredMachineConfigAnnotationKey:     targetConfigVersion,
				DesiredDrainerAnnotationKey:           "uncordon-xxx",
				LastAppliedDrainerAnnotationKey:       "uncordon-xxx",
				MachineConfigDaemonStateAnnotationKey: MachineConfigDaemonStateDone,
			},
		},
	}

	// Node still awaiting upgrade — keeps inPlaceUpgradeComplete false.
	upgradingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "upgrading-node",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: currentConfigVersion,
				DesiredMachineConfigAnnotationKey: currentConfigVersion,
			},
		},
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "token-test",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			TokenSecretPayloadKey: []byte("payload"),
		},
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(machineSet, machine, upgradingMachine).Build()
	guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(completedNode, upgradingNode).Build()

	r := &Reconciler{
		client:                 mgmtClient,
		guestClusterClient:     guestClient,
		CreateOrUpdateProvider: upsert.New(false),
	}

	upgradeAPI := &nodePoolUpgradeAPI{
		spec: struct {
			targetConfigVersion string
			poolRef             *capiv1.MachineSet
		}{
			targetConfigVersion: targetConfigVersion,
			poolRef:             machineSet,
		},
		status: struct {
			currentConfigVersion string
		}{
			currentConfigVersion: currentConfigVersion,
		},
	}

	err := r.reconcileInPlaceUpgrade(t.Context(), upgradeAPI, tokenSecret, "mco-image", nodePoolVersion)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the Machine was annotated with the NodePool version, not the HCP version.
	updatedMachine := &capiv1.Machine{}
	err = mgmtClient.Get(t.Context(), client.ObjectKeyFromObject(machine), updatedMachine)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedMachine.Annotations[hyperv1.NodePoolReleaseVersionAnnotation]).To(Equal(nodePoolVersion))
}

func TestReconcileInPlaceUpgradeDegradedNodeErrorMessage(t *testing.T) {
	_ = capiv1.AddToScheme(scheme.Scheme)

	targetConfigVersion := "target-hash"
	currentConfigVersion := "current-hash"
	degradedReason := "disk validation failed: node disk usage exceeds threshold"

	selector := map[string]string{"pool": "test"}

	testCases := []struct {
		name        string
		nodeName    string
		mcdState    string
		mcdMessage  string
		expectError bool
	}{
		{
			name:        "when a node is degraded it should include the node name in the error message",
			nodeName:    "degraded-node-xyz",
			mcdState:    MachineConfigDaemonStateDegraded,
			mcdMessage:  degradedReason,
			expectError: true,
		},
		{
			name:        "when a node is not degraded it should not return a degraded error",
			nodeName:    "healthy-node",
			mcdState:    MachineConfigDaemonStateDone,
			mcdMessage:  "",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			annotations := map[string]string{
				nodePoolAnnotationTargetConfigVersion:  targetConfigVersion,
				nodePoolAnnotationCurrentConfigVersion: currentConfigVersion,
			}
			if tc.expectError {
				annotations[nodePoolAnnotationUpgradeInProgressTrue] = "upgrade in progress"
			}

			machineSet := &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-ms",
					Namespace:   "test-ns",
					UID:         "ms-uid",
					Annotations: annotations,
				},
				Spec: capiv1.MachineSetSpec{
					Selector: metav1.LabelSelector{MatchLabels: selector},
				},
			}

			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "test-ns",
					Labels:    selector,
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind:       "MachineSet",
							Name:       machineSet.Name,
							UID:        machineSet.UID,
							Controller: ptr.To(true),
						},
					},
				},
				Status: capiv1.MachineStatus{
					NodeRef: capiv1.MachineNodeReference{Name: tc.nodeName},
				},
			}

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: tc.nodeName,
					Annotations: map[string]string{
						CurrentMachineConfigAnnotationKey:       currentConfigVersion,
						DesiredMachineConfigAnnotationKey:       targetConfigVersion,
						MachineConfigDaemonStateAnnotationKey:   tc.mcdState,
						MachineConfigDaemonMessageAnnotationKey: tc.mcdMessage,
					},
				},
			}

			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-test",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					TokenSecretPayloadKey: []byte("payload"),
				},
			}

			mgmtClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(machineSet, machine).Build()
			guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(node).Build()

			r := &Reconciler{
				client:                 mgmtClient,
				guestClusterClient:     guestClient,
				CreateOrUpdateProvider: upsert.New(false),
			}

			upgradeAPI := &nodePoolUpgradeAPI{
				spec: struct {
					targetConfigVersion string
					poolRef             *capiv1.MachineSet
				}{
					targetConfigVersion: targetConfigVersion,
					poolRef:             machineSet,
				},
				status: struct {
					currentConfigVersion string
				}{
					currentConfigVersion: currentConfigVersion,
				},
			}

			err := r.reconcileInPlaceUpgrade(t.Context(), upgradeAPI, tokenSecret, "mco-image", "4.18.37")

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.nodeName),
					"error message should contain the degraded node name")
				g.Expect(err.Error()).To(ContainSubstring(tc.mcdMessage),
					"error message should contain the MCD degraded reason")

				updatedMS := &capiv1.MachineSet{}
				err = mgmtClient.Get(t.Context(), client.ObjectKeyFromObject(machineSet), updatedMS)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedMS.Annotations[nodePoolAnnotationUpgradeInProgressFalse]).To(
					ContainSubstring(tc.nodeName),
					"MachineSet annotation should contain the degraded node name")
				_, hasTrue := updatedMS.Annotations[nodePoolAnnotationUpgradeInProgressTrue]
				g.Expect(hasTrue).To(BeFalse(),
					"upgradeInProgressTrue annotation should be deleted on degradation")
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				updatedMS := &capiv1.MachineSet{}
				err = mgmtClient.Get(t.Context(), client.ObjectKeyFromObject(machineSet), updatedMS)
				g.Expect(err).ToNot(HaveOccurred())
				_, hasDegraded := updatedMS.Annotations[nodePoolAnnotationUpgradeInProgressFalse]
				g.Expect(hasDegraded).To(BeFalse(),
					"degraded annotation should not be set for non-degraded nodes")
			}
		})
	}
}

type fakeReleaseProvider struct {
	image string
}

func (f *fakeReleaseProvider) Lookup(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	return &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: MachineConfigOperatorImage,
						From: &corev1.ObjectReference{Name: f.image},
					},
				},
			},
		},
	}, nil
}

func TestReconcileReturnsRequeueAfterDuringUpgrade(t *testing.T) {
	g := NewWithT(t)
	_ = capiv1.AddToScheme(scheme.Scheme)
	_ = hyperv1.AddToScheme(scheme.Scheme)

	hcpNamespace := "test-ns"
	hcpName := "test-hcp"
	targetConfig := "target-hash"
	currentConfig := "current-hash"
	selector := map[string]string{"pool": "test"}

	machineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ms",
			Namespace: hcpNamespace,
			UID:       "ms-uid",
			Annotations: map[string]string{
				nodePoolAnnotationTargetConfigVersion:  targetConfig,
				nodePoolAnnotationCurrentConfigVersion: currentConfig,
			},
		},
		Spec: capiv1.MachineSetSpec{
			Selector: metav1.LabelSelector{MatchLabels: selector},
		},
	}

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: hcpNamespace,
			Labels:    selector,
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "MachineSet",
					Name:       machineSet.Name,
					UID:        machineSet.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Status: capiv1.MachineStatus{
			NodeRef: capiv1.MachineNodeReference{Name: "test-node"},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: currentConfig,
				DesiredMachineConfigAnnotationKey: currentConfig,
			},
		},
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("token-%s-%s", machineSet.Name, targetConfig),
			Namespace: hcpNamespace,
		},
		Data: map[string][]byte{
			TokenSecretPayloadKey:        []byte("payload"),
			TokenSecretReleaseVersionKey: []byte("4.18.0"),
		},
	}

	hcp := manifests.HostedControlPlane(hcpNamespace, hcpName)
	hcp.Spec.ReleaseImage = "release-image:latest"

	pullSecret := manifests.PullSecret(hcpNamespace)
	pullSecret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(`{}`),
	}

	mgmtClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(machineSet, machine, tokenSecret, hcp, pullSecret).Build()
	guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(node).Build()

	r := &Reconciler{
		client:                 mgmtClient,
		guestClusterClient:     guestClient,
		releaseProvider:        &fakeReleaseProvider{image: "mco-image:latest"},
		hcpName:                hcpName,
		hcpNamespace:           hcpNamespace,
		CreateOrUpdateProvider: upsert.New(false),
	}

	result, err := r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      machineSet.Name,
			Namespace: machineSet.Namespace,
		},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(upgradeRequeueInterval))
}

func TestReconcileUpgradePodsReturnsErrorWhenDeleteFails(t *testing.T) {
	g := NewWithT(t)
	_ = capiv1.AddToScheme(scheme.Scheme)

	poolName := "test-pool"
	mcoImage := "mco-image:latest"
	namespace := inPlaceUpgradeNamespace(poolName)
	targetConfig := "target-hash"

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: "old-hash",
				DesiredMachineConfigAnnotationKey: targetConfig,
			},
		},
	}

	existingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      "machine-config-daemon-node1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	deleteErr := fmt.Errorf("connection refused")
	guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(existingPod).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				if obj.GetName() == existingPod.Name {
					return deleteErr
				}
				return nil
			},
		}).
		Build()

	r := &Reconciler{
		CreateOrUpdateProvider: upsert.New(false),
	}

	err := r.reconcileUpgradePods(t.Context(), guestClient, []*corev1.Node{node}, poolName, mcoImage, nil)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("connection refused"))
}

func TestReconcileUpgradePodsMultiNodeMixedStates(t *testing.T) {
	g := NewWithT(t)
	_ = capiv1.AddToScheme(scheme.Scheme)

	poolName := "test-pool"
	mcoImage := "mco-image:latest"
	namespace := inPlaceUpgradeNamespace(poolName)
	targetConfig := "target-hash"

	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: "old-hash",
				DesiredMachineConfigAnnotationKey: targetConfig,
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node2",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: "old-hash",
				DesiredMachineConfigAnnotationKey: targetConfig,
			},
		},
	}

	terminatedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      "machine-config-daemon-node1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}
	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      "machine-config-daemon-node2",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(terminatedPod, runningPod).Build()

	r := &Reconciler{
		CreateOrUpdateProvider: upsert.New(false),
	}

	err := r.reconcileUpgradePods(t.Context(), guestClient, []*corev1.Node{node1, node2}, poolName, mcoImage, nil)
	g.Expect(err).ToNot(HaveOccurred())

	pod1 := inPlaceUpgradePod(namespace.Name, node1.Name)
	getErr1 := guestClient.Get(t.Context(), client.ObjectKeyFromObject(pod1), pod1)
	g.Expect(apierrors.IsNotFound(getErr1)).To(BeTrue(), "expected terminated pod on node1 to be deleted")

	pod2 := inPlaceUpgradePod(namespace.Name, node2.Name)
	getErr2 := guestClient.Get(t.Context(), client.ObjectKeyFromObject(pod2), pod2)
	g.Expect(getErr2).ToNot(HaveOccurred(), "expected running pod on node2 to be retained")
	g.Expect(pod2.Status.Phase).To(Equal(corev1.PodRunning))
}

func TestReconcileUpgradePodsDeleteNotFoundContinues(t *testing.T) {
	g := NewWithT(t)
	_ = capiv1.AddToScheme(scheme.Scheme)

	poolName := "test-pool"
	mcoImage := "mco-image:latest"
	namespace := inPlaceUpgradeNamespace(poolName)
	targetConfig := "target-hash"

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Annotations: map[string]string{
				CurrentMachineConfigAnnotationKey: "old-hash",
				DesiredMachineConfigAnnotationKey: targetConfig,
			},
		},
	}

	existingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      "machine-config-daemon-node1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}

	guestClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(existingPod).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				if obj.GetName() == existingPod.Name {
					return apierrors.NewNotFound(corev1.Resource("pods"), existingPod.Name)
				}
				return nil
			},
		}).
		Build()

	r := &Reconciler{
		CreateOrUpdateProvider: upsert.New(false),
	}

	err := r.reconcileUpgradePods(t.Context(), guestClient, []*corev1.Node{node}, poolName, mcoImage, nil)
	g.Expect(err).ToNot(HaveOccurred(), "expected NotFound on Delete to be handled gracefully")
}
