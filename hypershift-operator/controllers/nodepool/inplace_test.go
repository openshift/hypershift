package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetNodesForMachineSet(t *testing.T) {
	capiv1.AddToScheme(scheme.Scheme)

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
						Controller: pointer.Bool(true),
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
						Controller: pointer.Bool(true),
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
						Controller: pointer.Bool(true),
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
