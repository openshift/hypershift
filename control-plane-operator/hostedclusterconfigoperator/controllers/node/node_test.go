package node

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestMain(m *testing.M) {
	_ = capiv1.AddToScheme(scheme.Scheme)
	os.Exit(m.Run())
}

func TestReconcileDeleteMachineAnnotation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                         string
		nodeAnnotations              map[string]string
		machineAnnotations           map[string]string
		machineDeletionTimestamp     *metav1.Time
		expectDeleteMachineOnMachine bool
	}{
		{
			name: "When Node has scale-down=true and Machine lacks delete-machine, it should set delete-machine on Machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "true",
			},
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: true,
		},
		{
			name:            "When Node lacks scale-down and Machine has delete-machine, it should remove delete-machine from Machine",
			nodeAnnotations: map[string]string{},
			machineAnnotations: map[string]string{
				capiv1.DeleteMachineAnnotation: "yes",
			},
			expectDeleteMachineOnMachine: false,
		},
		{
			name: "When Node has scale-down=true and Machine already has delete-machine, it should not modify Machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "true",
			},
			machineAnnotations: map[string]string{
				capiv1.DeleteMachineAnnotation: "yes",
			},
			expectDeleteMachineOnMachine: true,
		},
		{
			name:                         "When neither annotation is present, it should not modify Machine",
			nodeAnnotations:              map[string]string{},
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: false,
		},
		{
			name: "When Node scale-down value is not true, it should not set delete-machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "false",
			},
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: false,
		},
		{
			name: "When Node scale-down value is yes, it should not set delete-machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "yes",
			},
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: false,
		},
		{
			name: "When Node scale-down value is empty string, it should not set delete-machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "",
			},
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: false,
		},
		{
			name:                         "When Node has nil annotations, it should not modify Machine",
			nodeAnnotations:              nil,
			machineAnnotations:           map[string]string{},
			expectDeleteMachineOnMachine: false,
		},
		{
			name: "When Node has scale-down=true and Machine has nil annotations, it should set delete-machine on Machine",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "true",
			},
			machineAnnotations:           nil,
			expectDeleteMachineOnMachine: true,
		},
		{
			name: "When Machine is being deleted, it should skip",
			nodeAnnotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "true",
			},
			machineAnnotations:           map[string]string{},
			machineDeletionTimestamp:     &metav1.Time{Time: time.Now()},
			expectDeleteMachineOnMachine: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "test-ns",
					Name:              "test-machine",
					Annotations:       tc.machineAnnotations,
					DeletionTimestamp: tc.machineDeletionTimestamp,
				},
			}
			if tc.machineDeletionTimestamp != nil {
				machine.Finalizers = []string{"test-finalizer"}
			}

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: tc.nodeAnnotations,
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(machine).
				Build()

			r := &reconciler{client: c}
			err := r.reconcileDeleteMachineAnnotation(t.Context(), node, machine)
			g.Expect(err).ToNot(HaveOccurred())

			updatedMachine := &capiv1.Machine{}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(machine), updatedMachine)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasDeleteAnnotation := updatedMachine.Annotations[capiv1.DeleteMachineAnnotation]
			g.Expect(hasDeleteAnnotation).To(Equal(tc.expectDeleteMachineOnMachine))
		})
	}
}

func TestReconcileDeleteMachineAnnotation_PatchError(t *testing.T) {
	t.Parallel()

	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-ns",
			Name:        "test-machine",
			Annotations: map[string]string{},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Annotations: map[string]string{
				hyperv1.NodeScaleDownAnnotation: "true",
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(machine).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return fmt.Errorf("simulated patch failure")
			},
		}).
		Build()

	r := &reconciler{client: c}
	g := NewWithT(t)
	err := r.reconcileDeleteMachineAnnotation(t.Context(), node, machine)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to set delete-machine annotation"))
}

func TestGetMachineForNode(t *testing.T) {
	t.Parallel()

	machineNamespace := "test"
	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineNamespace,
			Name:      "test-machine",
		},
	}

	testCases := []struct {
		name        string
		node        *corev1.Node
		expectError bool
	}{
		{
			name: "When MachineAnnotation does not exist in Node, it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
					},
				},
			},
			expectError: true,
		},
		{
			name: "When ClusterNamespaceAnnotation does not exist in Node, it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.MachineAnnotation: machine.Name,
					},
				},
			},
			expectError: true,
		},
		{
			name: "When Machine does not exist, it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
						capiv1.MachineAnnotation:          "doesNotExist",
					},
				},
			},
			expectError: true,
		},
		{
			name: "When all annotations and Machine exist, it should return the Machine",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
						capiv1.MachineAnnotation:          machine.Name,
					},
				},
			},
			expectError: false,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(machine).
		Build()
	r := &reconciler{client: c}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			got, err := r.getMachineForNode(t.Context(), tc.node)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(got.Name).To(Equal(machine.Name))
				g.Expect(got.Namespace).To(Equal(machine.Namespace))
			}
		})
	}
}

func TestGetManagedLabels(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	labels := map[string]string{
		labelManagedPrefix + "." + "foo": "bar",
		"not-managed":                    "true",
	}

	managedLabels := getManagedLabels(labels)
	g.Expect(managedLabels).To(BeEquivalentTo(map[string]string{
		"foo": "bar",
	}))
}

// Verify supportutil.ParseNamespacedName extracts the Name portion from a "namespace/name" string.
func TestParseNamespacedNameForNodePoolLabel(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	g.Expect(supportutil.ParseNamespacedName("ns/name").Name).To(Equal("name"))
}
