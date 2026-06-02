package node

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testMachineNamespace = "test-ns"
	testNodePoolName     = "test-ns/test-nodepool"
	testMachineName      = "test-machine"
	testNodeName         = "test-node"
	testGlobalPSLabel    = "hypershift.openshift.io/nodepool-globalps-enabled"
)

func newTestNode(extraAnnotations, labels map[string]string, taints ...corev1.Taint) *corev1.Node {
	annotations := map[string]string{
		capiv1.ClusterNamespaceAnnotation: testMachineNamespace,
		capiv1.MachineAnnotation:          testMachineName,
	}
	maps.Copy(annotations, extraAnnotations)
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testNodeName,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: corev1.NodeSpec{
			Taints: taints,
		},
	}
}

func newTestMachine(annotations map[string]string, labels map[string]string) *capiv1.Machine {
	baseAnnotations := map[string]string{
		nodePoolAnnotation: testNodePoolName,
	}
	maps.Copy(baseAnnotations, annotations)
	return &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   testMachineNamespace,
			Name:        testMachineName,
			Annotations: baseAnnotations,
			Labels:      labels,
		},
	}
}

func taintsJSON(taints []corev1.Taint) string {
	data, _ := json.Marshal(taints)
	return string(data)
}

func managedLabel(key string) string {
	return labelManagedPrefix + "." + key
}

func TestNodePoolNameFromMachine(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		machine              *capiv1.Machine
		expectedNodePoolName string
		expectError          bool
	}{
		{
			name: "When nodePoolAnnotation does not exist in Machine it should fail",
			machine: &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "no-annotation",
				},
			},
			expectedNodePoolName: "",
			expectError:          true,
		},
		{
			name: "When nodePoolAnnotation is empty it should fail",
			machine: &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "empty-annotation",
					Annotations: map[string]string{
						nodePoolAnnotation: "",
					},
				},
			},
			expectedNodePoolName: "",
			expectError:          true,
		},
		{
			name: "When nodePoolAnnotation exists it should return the NodePool Name",
			machine: &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "has-annotation",
					Annotations: map[string]string{
						nodePoolAnnotation: "ns/name",
					},
				},
			},
			expectedNodePoolName: supportutil.ParseNamespacedName("ns/name").Name,
			expectError:          false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			got, err := nodePoolNameFromMachine(tc.machine)
			g.Expect(got).To(Equal(tc.expectedNodePoolName))
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestGetManagedLabels(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	labels := map[string]string{
		managedLabel("foo"): "bar",
		"not-managed":       "true",
	}

	managedLabels := getManagedLabels(labels)
	g.Expect(managedLabels).To(BeEquivalentTo(map[string]string{
		"foo": "bar",
	}))
}

func TestComputeSyncHash(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		labels map[string]string
		taints []corev1.Taint
		verify func(g Gomega, hash string, err error)
	}{
		{
			name:   "When labels and taints are empty it should return a stable hash",
			labels: map[string]string{},
			taints: []corev1.Taint{},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hash).NotTo(BeEmpty())
				second, err2 := computeSyncHash(map[string]string{}, []corev1.Taint{})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).To(Equal(second))
			},
		},
		{
			name:   "When labels are nil it should return a stable hash",
			labels: nil,
			taints: nil,
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(hash).NotTo(BeEmpty())
			},
		},
		{
			name: "When labels are provided it should produce a deterministic hash",
			labels: map[string]string{
				"foo": "bar",
				"baz": "qux",
			},
			taints: []corev1.Taint{},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				second, err2 := computeSyncHash(map[string]string{"baz": "qux", "foo": "bar"}, []corev1.Taint{})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).To(Equal(second))
			},
		},
		{
			name:   "When taints are provided it should produce a deterministic hash",
			labels: map[string]string{},
			taints: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
			},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				reversed, err2 := computeSyncHash(map[string]string{}, []corev1.Taint{
					{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
					{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).To(Equal(reversed))
			},
		},
		{
			name: "When labels differ it should produce different hashes",
			labels: map[string]string{
				"foo": "bar",
			},
			taints: []corev1.Taint{},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				different, err2 := computeSyncHash(map[string]string{"foo": "changed"}, []corev1.Taint{})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).NotTo(Equal(different))
			},
		},
		{
			name:   "When taints differ it should produce different hashes",
			labels: map[string]string{},
			taints: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				different, err2 := computeSyncHash(map[string]string{}, []corev1.Taint{
					{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoExecute},
				})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).NotTo(Equal(different))
			},
		},
		{
			name: "When a new label is added it should produce a different hash",
			labels: map[string]string{
				"existing": "label",
			},
			taints: []corev1.Taint{},
			verify: func(g Gomega, hash string, err error) {
				g.Expect(err).NotTo(HaveOccurred())
				withNewLabel, err2 := computeSyncHash(map[string]string{
					"existing":        "label",
					testGlobalPSLabel: "true",
				}, []corev1.Taint{})
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(hash).NotTo(Equal(withNewLabel))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hash, err := computeSyncHash(tc.labels, tc.taints)
			tc.verify(g, hash, err)
		})
	}
}

func TestLabelsHaveSynced(t *testing.T) {
	t.Parallel()

	expectedHash, _ := computeSyncHash(map[string]string{"foo": "bar"}, []corev1.Taint{})

	testCases := []struct {
		name     string
		node     *corev1.Node
		expected bool
	}{
		{
			name: "When annotation matches expected hash it should return true",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						labelsSyncedAnnotation: expectedHash,
					},
				},
			},
			expected: true,
		},
		{
			name: "When annotation has different hash it should return false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						labelsSyncedAnnotation: "differenthashvalue",
					},
				},
			},
			expected: false,
		},
		{
			name: "When annotation has legacy value true it should return false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						labelsSyncedAnnotation: "true",
					},
				},
			},
			expected: false,
		},
		{
			name: "When annotation is empty string it should return false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						labelsSyncedAnnotation: "",
					},
				},
			},
			expected: false,
		},
		{
			name: "When annotation does not exist it should return false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: false,
		},
		{
			name: "When annotations map is nil it should return false",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(labelsHaveSynced(tc.node, expectedHash)).To(Equal(tc.expected))
		})
	}
}

func TestMergeTaints(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		existing []corev1.Taint
		desired  []corev1.Taint
		expected []corev1.Taint
	}{
		{
			name:     "When both slices are empty it should return empty",
			existing: []corev1.Taint{},
			desired:  []corev1.Taint{},
			expected: []corev1.Taint{},
		},
		{
			name:     "When existing is nil it should return desired",
			existing: nil,
			desired: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "When desired is empty it should return existing unchanged",
			existing: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			desired: []corev1.Taint{},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "When desired has new taints it should append them",
			existing: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			desired: []corev1.Taint{
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
			},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
			},
		},
		{
			name: "When desired has duplicate taints it should not duplicate them",
			existing: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			desired: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "When desired has same key but different effect it should add it",
			existing: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			},
			desired: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoExecute},
			},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoExecute},
			},
		},
		{
			name: "When desired has mix of new and existing taints it should only add new ones",
			existing: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
			},
			desired: []corev1.Taint{
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
				{Key: "key3", Value: "val3", Effect: corev1.TaintEffectPreferNoSchedule},
			},
			expected: []corev1.Taint{
				{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key2", Value: "val2", Effect: corev1.TaintEffectNoExecute},
				{Key: "key3", Value: "val3", Effect: corev1.TaintEffectPreferNoSchedule},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := mergeTaints(tc.existing, tc.desired)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestReconcile(t *testing.T) {
	_ = capiv1.AddToScheme(scheme.Scheme)
	t.Parallel()

	testCases := []struct {
		name           string
		node           *corev1.Node
		machine        *capiv1.Machine
		expectedResult reconcile.Result
		expectError    bool
		verify         func(g Gomega, node *corev1.Node)
	}{
		{
			name: "When labels have not been synced it should sync labels and taints and set hash annotation",
			node: newTestNode(nil, map[string]string{}),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: taintsJSON([]corev1.Taint{{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule}})},
				map[string]string{managedLabel(testGlobalPSLabel): "true"},
			),
			verify: func(g Gomega, node *corev1.Node) {
				g.Expect(node.Labels).To(HaveKeyWithValue(testGlobalPSLabel, "true"))
				g.Expect(node.Labels).To(HaveKeyWithValue(hyperv1.NodePoolLabel, "test-nodepool"))
				g.Expect(node.Spec.Taints).To(ContainElement(corev1.Taint{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule}))
				g.Expect(node.Annotations[labelsSyncedAnnotation]).NotTo(Equal("true"))
				g.Expect(node.Annotations[labelsSyncedAnnotation]).NotTo(BeEmpty())
			},
		},
		{
			name: "When labels have legacy value true it should re-sync and update to hash",
			node: newTestNode(
				map[string]string{labelsSyncedAnnotation: "true"},
				map[string]string{hyperv1.NodePoolLabel: "test-nodepool"},
			),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: taintsJSON([]corev1.Taint{})},
				map[string]string{managedLabel(testGlobalPSLabel): "true"},
			),
			verify: func(g Gomega, node *corev1.Node) {
				g.Expect(node.Labels).To(HaveKeyWithValue(testGlobalPSLabel, "true"))
				g.Expect(node.Annotations[labelsSyncedAnnotation]).NotTo(Equal("true"))
				g.Expect(node.Annotations[labelsSyncedAnnotation]).NotTo(BeEmpty())
			},
		},
		{
			name: "When hash matches it should not modify the node",
			node: func() *corev1.Node {
				labels := map[string]string{hyperv1.NodePoolLabel: "test-nodepool"}
				hash, _ := computeSyncHash(labels, []corev1.Taint{})
				return newTestNode(map[string]string{labelsSyncedAnnotation: hash}, labels)
			}(),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: taintsJSON([]corev1.Taint{})},
				map[string]string{},
			),
			verify: func(g Gomega, node *corev1.Node) {
				g.Expect(node.Labels).NotTo(HaveKey(testGlobalPSLabel))
			},
		},
		{
			name: "When hash mismatches due to new Machine label it should re-sync",
			node: func() *corev1.Node {
				oldLabels := map[string]string{hyperv1.NodePoolLabel: "test-nodepool"}
				oldHash, _ := computeSyncHash(oldLabels, []corev1.Taint{})
				return newTestNode(map[string]string{labelsSyncedAnnotation: oldHash}, oldLabels)
			}(),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: taintsJSON([]corev1.Taint{})},
				map[string]string{managedLabel(testGlobalPSLabel): "true"},
			),
			verify: func(g Gomega, node *corev1.Node) {
				g.Expect(node.Labels).To(HaveKeyWithValue(testGlobalPSLabel, "true"))
				newExpectedLabels := map[string]string{
					hyperv1.NodePoolLabel: "test-nodepool",
					testGlobalPSLabel:     "true",
				}
				newHash, err := computeSyncHash(newExpectedLabels, []corev1.Taint{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(node.Annotations[labelsSyncedAnnotation]).To(Equal(newHash))
			},
		},
		{
			name: "When re-syncing with existing taints it should not duplicate them",
			node: newTestNode(
				map[string]string{labelsSyncedAnnotation: "true"},
				map[string]string{},
				corev1.Taint{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule},
			),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: taintsJSON([]corev1.Taint{{Key: "key1", Value: "val1", Effect: corev1.TaintEffectNoSchedule}})},
				map[string]string{},
			),
			verify: func(g Gomega, node *corev1.Node) {
				count := 0
				for _, t := range node.Spec.Taints {
					if t.Key == "key1" && t.Value == "val1" && t.Effect == corev1.TaintEffectNoSchedule {
						count++
					}
				}
				g.Expect(count).To(Equal(1))
			},
		},
		{
			name:    "When Machine has no taints annotation it should sync labels without error",
			node:    newTestNode(nil, map[string]string{}),
			machine: newTestMachine(nil, map[string]string{managedLabel(testGlobalPSLabel): "true"}),
			verify: func(g Gomega, node *corev1.Node) {
				g.Expect(node.Labels).To(HaveKeyWithValue(testGlobalPSLabel, "true"))
				g.Expect(node.Labels).To(HaveKeyWithValue(hyperv1.NodePoolLabel, "test-nodepool"))
				g.Expect(node.Annotations[labelsSyncedAnnotation]).NotTo(BeEmpty())
			},
		},
		{
			name: "When Machine has invalid taints JSON it should return an error",
			node: newTestNode(nil, map[string]string{}),
			machine: newTestMachine(
				map[string]string{nodePoolAnnotationTaints: "not-valid-json"},
				map[string]string{},
			),
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			mgmtClient := fake.NewClientBuilder().WithObjects(tc.machine).Build()
			guestClient := fake.NewClientBuilder().WithObjects(tc.node).Build()

			r := &reconciler{
				client:                 mgmtClient,
				guestClusterClient:     guestClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdate{},
			}

			result, err := r.Reconcile(t.Context(), reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(tc.node),
			})

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			if tc.expectedResult != (reconcile.Result{}) {
				g.Expect(result).To(Equal(tc.expectedResult))
			}

			if tc.verify != nil {
				updatedNode := &corev1.Node{}
				err := guestClient.Get(t.Context(), client.ObjectKeyFromObject(tc.node), updatedNode)
				g.Expect(err).NotTo(HaveOccurred())
				tc.verify(g, updatedNode)
			}
		})
	}
}

// simpleCreateOrUpdate implements CreateOrUpdateProvider with a
// get-mutate-update cycle for unit tests without server-side apply.
type simpleCreateOrUpdate struct{}

func (s *simpleCreateOrUpdate) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return controllerutil.OperationResultNone, err
	}
	if err := f(); err != nil {
		return controllerutil.OperationResultNone, err
	}
	if err := c.Update(ctx, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}
	return controllerutil.OperationResultUpdated, nil
}
