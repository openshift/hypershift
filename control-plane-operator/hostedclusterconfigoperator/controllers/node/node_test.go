package node

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNodeToNodePoolName(t *testing.T) {
	_ = capiv1.AddToScheme(scheme.Scheme)

	machineNamespace := "test"
	nodePoolName := "ns/name"
	machineWithNodePoolAnnotation := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineNamespace,
			Name:      "hasNodePoolAnnotation",
			Annotations: map[string]string{
				nodePoolAnnotation: nodePoolName,
			},
		},
	}
	machineWithOutNodePoolAnnotation := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineNamespace,
			Name:      "DoNotHaveNodePoolAnnotation",
		},
	}

	testCases := []struct {
		name                 string
		node                 *corev1.Node
		expectedNodePoolName string
		error                bool
	}{
		{
			name: "When MachineAnnotation does not exist in Node it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
					},
				},
			},
			expectedNodePoolName: "",
			error:                true,
		},
		{
			name: "When ClusterNamespaceAnnotation does not exist in Node it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.MachineAnnotation: machineWithNodePoolAnnotation.Name,
					},
				},
			},
			expectedNodePoolName: "",
			error:                true,
		},
		{
			name: "When nodePoolAnnotation does not exist in Machine it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
						capiv1.MachineAnnotation:          machineWithOutNodePoolAnnotation.Name,
					},
				},
			},
			expectedNodePoolName: "",
			error:                true,
		},
		{
			name: "When Machine does not exist it should fail",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
						capiv1.MachineAnnotation:          "doesNotExist",
					},
				},
			},
			expectedNodePoolName: "",
			error:                true,
		},
		{
			name: "When all annotations and Machine exist it should return the NodePool Name",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.ClusterNamespaceAnnotation: machineNamespace,
						capiv1.MachineAnnotation:          machineWithNodePoolAnnotation.Name,
					},
				},
			},
			expectedNodePoolName: supportutil.ParseNamespacedName(nodePoolName).Name,
			error:                false,
		},
	}

	c := fake.NewClientBuilder().WithObjects(machineWithNodePoolAnnotation, machineWithOutNodePoolAnnotation).Build()
	r := &reconciler{
		client: c,
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			got, err := r.nodeToNodePoolName(context.Background(), tc.node)
			g.Expect(got).To(Equal(tc.expectedNodePoolName))
			g.Expect(err != nil).To(Equal(tc.error))
		})
	}
}

func TestGetManagedLabels(t *testing.T) {
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
