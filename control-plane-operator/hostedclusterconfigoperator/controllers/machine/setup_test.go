package machine

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/client/clientset/clientset/scheme"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kubevirtv1 "kubevirt.io/api/core/v1"
)

func TestRequestsForVirtualMachineInstance(t *testing.T) {
	registerMachineTestScheme()

	matching := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "worker1"},
		Spec: capiv1.MachineSpec{
			InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "vm-worker1"},
		},
	}
	other := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "worker2"},
		Spec: capiv1.MachineSpec{
			InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "vm-worker2"},
		},
	}
	r := &reconciler{
		client: fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(matching, other).Build(),
	}
	vmi := &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "vm-worker1"},
	}

	t.Run("When the VirtualMachineInstance name matches a Machine, it should enqueue that Machine", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(r.requestsForVirtualMachineInstance(t.Context(), vmi)).To(Equal([]reconcile.Request{{
			NamespacedName: client.ObjectKeyFromObject(matching),
		}}))
	})

	t.Run("When the VirtualMachineInstance name matches no Machine, it should enqueue nothing", func(t *testing.T) {
		g := NewWithT(t)
		unrelated := vmi.DeepCopy()
		unrelated.Name = "vm-unknown"
		g.Expect(r.requestsForVirtualMachineInstance(t.Context(), unrelated)).To(BeEmpty())
	})
}

func TestVirtualMachineInstanceNodeChanged(t *testing.T) {
	predicate := virtualMachineInstanceNodeChanged()
	withNode := func(nodeName string) *kubevirtv1.VirtualMachineInstance {
		return &kubevirtv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-worker1"},
			Status:     kubevirtv1.VirtualMachineInstanceStatus{NodeName: nodeName},
		}
	}

	testCases := []struct {
		name   string
		allow  bool
		create *kubevirtv1.VirtualMachineInstance
		old    *kubevirtv1.VirtualMachineInstance
		new    *kubevirtv1.VirtualMachineInstance
		delete client.Object
	}{
		{
			name:   "When a VirtualMachineInstance is created with a node, it should reconcile",
			allow:  true,
			create: withNode("mgmt-node-1"),
		},
		{
			name:   "When a VirtualMachineInstance is created without a node, it should not reconcile",
			allow:  false,
			create: withNode(""),
		},
		{
			name:  "When the VirtualMachineInstance node changes, it should reconcile",
			allow: true,
			old:   withNode("mgmt-node-1"),
			new:   withNode("mgmt-node-2"),
		},
		{
			name:  "When the VirtualMachineInstance node is unchanged, it should not reconcile",
			allow: false,
			old:   withNode("mgmt-node-1"),
			new:   withNode("mgmt-node-1"),
		},
		{
			name:   "When a scheduled VirtualMachineInstance is deleted, it should reconcile",
			allow:  true,
			delete: withNode("mgmt-node-1"),
		},
		{
			name:   "When an unscheduled VirtualMachineInstance is deleted, it should not reconcile",
			allow:  false,
			delete: withNode(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			switch {
			case tc.create != nil:
				g.Expect(predicate.Create(event.CreateEvent{Object: tc.create})).To(Equal(tc.allow))
			case tc.delete != nil:
				g.Expect(predicate.Delete(event.DeleteEvent{Object: tc.delete})).To(Equal(tc.allow))
			default:
				g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: tc.old, ObjectNew: tc.new})).To(Equal(tc.allow))
			}
		})
	}
}
