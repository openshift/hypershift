package spotremediation

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = capiv1.AddToScheme(s)
	return s
}

func TestReconcile(t *testing.T) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	const (
		machineNamespace = "clusters-test"
		machineName      = "worker-1"
		nodeName         = "ip-10-0-1-100.ec2.internal"
	)

	baseNode := func() *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Annotations: map[string]string{
					capiv1.MachineAnnotation:          machineName,
					capiv1.ClusterNamespaceAnnotation: machineNamespace,
				},
			},
		}
	}

	baseMachine := func() *capiv1.Machine {
		return &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: machineNamespace,
				Name:      machineName,
				Labels: map[string]string{
					interruptibleInstanceLabel: "",
				},
			},
		}
	}

	testCases := []struct {
		name            string
		node            *corev1.Node
		machine         *capiv1.Machine
		expectDeleted   bool
		expectAnnotated bool
	}{
		{
			name:            "When node has no NTH taint it should not delete the machine",
			node:            baseNode(),
			machine:         baseMachine(),
			expectDeleted:   false,
			expectAnnotated: false,
		},
		{
			name: "When node has NTH taint and machine is interruptible it should delete the machine",
			node: func() *corev1.Node {
				n := baseNode()
				n.Spec.Taints = []corev1.Taint{
					{Key: "aws-node-termination-handler/rebalance-recommendation", Effect: corev1.TaintEffectNoSchedule},
				}
				return n
			}(),
			machine:         baseMachine(),
			expectDeleted:   true,
			expectAnnotated: true,
		},
		{
			name: "When node has NTH taint and machine is not interruptible it should not delete the machine",
			node: func() *corev1.Node {
				n := baseNode()
				n.Spec.Taints = []corev1.Taint{
					{Key: "aws-node-termination-handler/spot-itn", Effect: corev1.TaintEffectNoSchedule},
				}
				return n
			}(),
			machine: func() *capiv1.Machine {
				m := baseMachine()
				m.Labels = map[string]string{}
				return m
			}(),
			expectDeleted:   false,
			expectAnnotated: false,
		},
		{
			name: "When node has NTH taint and machine is already deleting it should not delete the machine",
			node: func() *corev1.Node {
				n := baseNode()
				n.Spec.Taints = []corev1.Taint{
					{Key: "aws-node-termination-handler/spot-itn", Effect: corev1.TaintEffectNoSchedule},
				}
				return n
			}(),
			machine: func() *capiv1.Machine {
				m := baseMachine()
				now := metav1.NewTime(time.Now())
				m.DeletionTimestamp = &now
				m.Finalizers = []string{"test-finalizer"}
				return m
			}(),
			expectDeleted:   false,
			expectAnnotated: false,
		},
		{
			name:            "When node is not found it should return no error",
			node:            nil,
			machine:         nil,
			expectDeleted:   false,
			expectAnnotated: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := testScheme()

			guestObjects := []client.Object{}
			if tc.node != nil {
				guestObjects = append(guestObjects, tc.node)
			}

			mgmtObjects := []client.Object{}
			if tc.machine != nil {
				mgmtObjects = append(mgmtObjects, tc.machine)
			}

			guestClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(guestObjects...).Build()
			mgmtClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mgmtObjects...).Build()

			r := &reconciler{
				client:             mgmtClient,
				guestClusterClient: guestClient,
			}

			result, err := r.Reconcile(t.Context(), reconcile.Request{
				NamespacedName: types.NamespacedName{Name: nodeName},
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(ctrl.Result{}))

			if tc.machine == nil {
				return
			}

			machine := &capiv1.Machine{}
			getErr := mgmtClient.Get(t.Context(), client.ObjectKeyFromObject(tc.machine), machine)

			if tc.expectDeleted {
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "expected machine to be deleted")
			} else {
				g.Expect(getErr).NotTo(HaveOccurred(), "expected machine to still exist")
			}

			if tc.expectAnnotated && !tc.expectDeleted {
				g.Expect(machine.Annotations).To(HaveKey(spotInterruptionSignalAnnotation))
			}
		})
	}
}

func TestNthTaintKey(t *testing.T) {
	testCases := []struct {
		name     string
		node     *corev1.Node
		expected string
	}{
		{
			name: "When node has rebalance-recommendation taint it should return the taint key",
			node: &corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{Key: "aws-node-termination-handler/rebalance-recommendation", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			expected: "aws-node-termination-handler/rebalance-recommendation",
		},
		{
			name: "When node has spot-itn taint it should return the taint key",
			node: &corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{Key: "aws-node-termination-handler/spot-itn", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			expected: "aws-node-termination-handler/spot-itn",
		},
		{
			name: "When node has no NTH taints it should return empty string",
			node: &corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{Key: "node.kubernetes.io/not-ready", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			expected: "",
		},
		{
			name: "When node has no taints it should return empty string",
			node: &corev1.Node{
				Spec: corev1.NodeSpec{},
			},
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(nthTaintKey(tc.node)).To(Equal(tc.expected))
		})
	}
}
