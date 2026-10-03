package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = kubevirtv1.AddToScheme(s)
	_ = cdiv1beta1.AddToScheme(s)
	return s
}

func TestPropagateLabelsToInfraResources(t *testing.T) {
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)

	tests := []struct {
		name           string
		existingVMs    []kubevirtv1.VirtualMachine
		existingDVs    []cdiv1beta1.DataVolume
		desiredLabels  map[string]string
		expectVMLabels map[string]string
		expectDVLabels map[string]string
	}{
		{
			name: "When labels are desired, they should be applied to VMs and DataVolumes",
			existingVMs: []kubevirtv1.VirtualMachine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm-1",
						Namespace: infraNS,
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
							hyperv1.InfraIDLabel:      infraID,
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									hyperv1.NodePoolNameLabel: nodePoolName,
								},
							},
						},
					},
				},
			},
			existingDVs: []cdiv1beta1.DataVolume{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm-1-rhcos",
						Namespace: infraNS,
						Labels: map[string]string{
							hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
						},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "VirtualMachine", Name: "vm-1"},
						},
					},
				},
			},
			desiredLabels: map[string]string{
				"custom-key": "custom-value",
			},
			expectVMLabels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
				"custom-key":              "custom-value",
			},
			expectDVLabels: map[string]string{
				hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				"custom-key":                           "custom-value",
			},
		},
		{
			name: "When no VMs exist, it should complete without error",
			desiredLabels: map[string]string{
				"custom-key": "custom-value",
			},
		},
		{
			name: "When desired labels contain reserved keys, they should be skipped",
			existingVMs: []kubevirtv1.VirtualMachine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm-1",
						Namespace: infraNS,
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
							hyperv1.InfraIDLabel:      infraID,
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									hyperv1.NodePoolNameLabel: nodePoolName,
								},
							},
						},
					},
				},
			},
			desiredLabels: map[string]string{
				hyperv1.NodePoolNameLabel:              "should-not-overwrite",
				hyperv1.InfraIDLabel:                   "should-not-overwrite",
				hyperv1.IsKubeVirtRHCOSVolumeLabelName: "should-not-overwrite",
				"allowed-key":                          "allowed-value",
			},
			expectVMLabels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
				"allowed-key":             "allowed-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			var objs []client.Object
			for i := range tt.existingVMs {
				objs = append(objs, &tt.existingVMs[i])
			}
			for i := range tt.existingDVs {
				objs = append(objs, &tt.existingDVs[i])
			}
			cl := fake.NewClientBuilder().
				WithScheme(newScheme()).
				WithObjects(objs...).
				Build()

			err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, tt.desiredLabels)
			g.Expect(err).ToNot(HaveOccurred())
			if tt.expectVMLabels != nil {
				vm := &kubevirtv1.VirtualMachine{}
				g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, vm)).To(Succeed())
				for k, v := range tt.expectVMLabels {
					g.Expect(vm.Labels).To(HaveKeyWithValue(k, v), "VM ObjectMeta should have label %s=%s", k, v)
				}
			}
			if tt.expectDVLabels != nil {
				dv := &cdiv1beta1.DataVolume{}
				g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1-rhcos"}, dv)).To(Succeed())
				for k, v := range tt.expectDVLabels {
					g.Expect(dv.Labels).To(HaveKeyWithValue(k, v))
				}
			}
		})
	}
}

func TestPropagateLabelsToMultipleVMs(t *testing.T) {
	g := NewWithT(t)

	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)

	vms := []client.Object{
		&kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vm-1",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.NodePoolNameLabel: nodePoolName,
					hyperv1.InfraIDLabel:      infraID,
				},
			},
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
						},
					},
				},
			},
		},
		&kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vm-2",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.NodePoolNameLabel: nodePoolName,
					hyperv1.InfraIDLabel:      infraID,
				},
			},
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
						},
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vms...).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"team": "platform",
	})
	g.Expect(err).ToNot(HaveOccurred())

	for _, name := range []string{"vm-1", "vm-2"} {
		vm := &kubevirtv1.VirtualMachine{}
		g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: name}, vm)).To(Succeed())
		g.Expect(vm.Labels).To(HaveKeyWithValue("team", "platform"), "VM %s should have the propagated label", name)
	}
}

func TestPropagateLabelsReturnsAggregateErrorAndContinues(t *testing.T) {
	g := NewWithT(t)

	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)

	objs := []client.Object{
		&kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vm-fail",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.NodePoolNameLabel: nodePoolName,
					hyperv1.InfraIDLabel:      infraID,
				},
			},
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
						},
					},
				},
			},
		},
		&kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vm-ok",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.NodePoolNameLabel: nodePoolName,
					hyperv1.InfraIDLabel:      infraID,
				},
			},
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
						},
					},
				},
			},
		},
		&cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dv-ok",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "VirtualMachine", Name: "vm-ok"},
				},
			},
		},
		&cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dv-fail",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "VirtualMachine", Name: "vm-fail"},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if obj.GetName() == "vm-fail" || obj.GetName() == "dv-fail" {
					return fmt.Errorf("patch conflict")
				}
				return cl.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"team": "platform",
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to propagate labels to VM vm-fail"))
	g.Expect(err.Error()).To(ContainSubstring("failed to propagate labels to DataVolume dv-fail"))
	g.Expect(err.Error()).To(ContainSubstring("patch conflict"))

	okVM := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-ok"}, okVM)).To(Succeed())
	g.Expect(okVM.Labels).To(HaveKeyWithValue("team", "platform"), "healthy VM should still receive labels")

	okDV := &cdiv1beta1.DataVolume{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "dv-ok"}, okDV)).To(Succeed())
	g.Expect(okDV.Labels).To(HaveKeyWithValue("team", "platform"), "healthy DataVolume should still receive labels")
}

func TestVMITemplateLabelsArePropagated(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()
	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"env": "production",
	})
	g.Expect(err).ToNot(HaveOccurred())

	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue("env", "production"), "VM ObjectMeta labels should have the propagated label")
	g.Expect(updated.Spec.Template.ObjectMeta.Labels).To(HaveKeyWithValue("env", "production"), "VMI template labels should have the propagated label")
}

func TestNilVMITemplateLabelsAreInitialized(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()
	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"env": "production",
	})
	g.Expect(err).ToNot(HaveOccurred())

	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue("env", "production"), "VM ObjectMeta labels should have the propagated label")
	g.Expect(updated.Spec.Template.ObjectMeta.Labels).To(HaveKeyWithValue("env", "production"), "nil VMI template labels should be initialized and receive the propagated label")
}

func TestDVsNotOwnedByMatchingVMsAreSkipped(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	objs := []client.Object{
		&kubevirtv1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vm-1",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.NodePoolNameLabel: nodePoolName,
					hyperv1.InfraIDLabel:      infraID,
				},
			},
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							hyperv1.NodePoolNameLabel: nodePoolName,
						},
					},
				},
			},
		},
		&cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dv-owned-by-matching-vm",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "VirtualMachine", Name: "vm-1"},
				},
			},
		},
		&cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dv-owned-by-other-vm",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "VirtualMachine", Name: "vm-from-another-pool"},
				},
			},
		},
		&cdiv1beta1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dv-no-owner",
				Namespace: infraNS,
				Labels: map[string]string{
					hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(objs...).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"env": "staging",
	})
	g.Expect(err).ToNot(HaveOccurred())

	ownedDV := &cdiv1beta1.DataVolume{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "dv-owned-by-matching-vm"}, ownedDV)).To(Succeed())
	g.Expect(ownedDV.Labels).To(HaveKeyWithValue("env", "staging"), "DV owned by matching VM should get the label")
	otherDV := &cdiv1beta1.DataVolume{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "dv-owned-by-other-vm"}, otherDV)).To(Succeed())
	g.Expect(otherDV.Labels).ToNot(HaveKey("env"), "DV owned by a different VM should not get the label")
	noOwnerDV := &cdiv1beta1.DataVolume{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "dv-no-owner"}, noOwnerDV)).To(Succeed())
	g.Expect(noOwnerDV.Labels).ToNot(HaveKey("env"), "DV with no ownerReference should not get the label")
}

func TestNilAndEmptyDesiredLabels(t *testing.T) {
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
					},
				},
			},
		},
	}

	tests := []struct {
		name          string
		desiredLabels map[string]string
	}{
		{
			name:          "When desired labels are nil, it should not error",
			desiredLabels: nil,
		},
		{
			name:          "When desired labels are empty, it should not error",
			desiredLabels: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cl := fake.NewClientBuilder().
				WithScheme(newScheme()).
				WithObjects(vm.DeepCopy()).
				Build()

			err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, tt.desiredLabels)
			g.Expect(err).ToNot(HaveOccurred())

			updated := &kubevirtv1.VirtualMachine{}
			g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
			g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.NodePoolNameLabel, nodePoolName), "bookkeeping labels should be untouched")
			g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.InfraIDLabel, infraID), "bookkeeping labels should be untouched")
		})
	}
}

func TestManagedKeysAnnotationIsTracked(t *testing.T) {
	g := NewWithT(t)

	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
					},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()
	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"alpha": "a",
		"beta":  "b",
	})
	g.Expect(err).ToNot(HaveOccurred())
	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	raw, ok := updated.Annotations[ManagedHCLabelKeysAnnotation]
	g.Expect(ok).To(BeTrue(), "managed-keys annotation should be present")
	var managedKeys []string
	g.Expect(json.Unmarshal([]byte(raw), &managedKeys)).To(Succeed())
	g.Expect(managedKeys).To(Equal([]string{"alpha", "beta"}), "managed keys should be sorted and match the applied label keys")
}

func TestManagedKeysAnnotationRemovedWhenAllLabelsCleared(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	managedKeys, _ := json.Marshal([]string{"old-key"})
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
				"old-key":                 "old-value",
			},
			Annotations: map[string]string{
				ManagedHCLabelKeysAnnotation: string(managedKeys),
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
						"old-key":                 "old-value",
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{})
	g.Expect(err).ToNot(HaveOccurred())
	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	g.Expect(updated.Labels).ToNot(HaveKey("old-key"), "old managed label should be removed")
	g.Expect(updated.Annotations).ToNot(HaveKey(ManagedHCLabelKeysAnnotation), "managed-keys annotation should be removed when no keys remain")
}

func TestIdempotency(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()

	desired := map[string]string{"env": "prod"}
	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, desired)
	g.Expect(err).ToNot(HaveOccurred())

	first := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, first)).To(Succeed())
	firstRV := first.ResourceVersion
	err = PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, desired)
	g.Expect(err).ToNot(HaveOccurred())
	second := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, second)).To(Succeed())
	g.Expect(second.ResourceVersion).To(Equal(firstRV), "second call with same labels should be a no-op (no patch issued)")
}

func TestLabelRemoval(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	managedKeys, _ := json.Marshal([]string{"old-key", "staying-key"})
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
				"old-key":                 "old-value",
				"staying-key":             "staying-value",
			},
			Annotations: map[string]string{
				ManagedHCLabelKeysAnnotation: string(managedKeys),
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
						"old-key":                 "old-value",
						"staying-key":             "staying-value",
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"staying-key": "staying-value",
	})
	g.Expect(err).ToNot(HaveOccurred())
	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue("staying-key", "staying-value"))
	g.Expect(updated.Labels).ToNot(HaveKey("old-key"))
	g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.NodePoolNameLabel, nodePoolName))
}

func TestTemplateLabelRemoval(t *testing.T) {
	g := NewWithT(t)
	const (
		infraNS      = "clusters-test"
		nodePoolName = "test-nodepool"
		infraID      = "test-infra-id"
	)
	managedKeys, _ := json.Marshal([]string{"old-key", "staying-key"})
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vm-1",
			Namespace: infraNS,
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: nodePoolName,
				hyperv1.InfraIDLabel:      infraID,
				"old-key":                 "old-value",
				"staying-key":             "staying-value",
			},
			Annotations: map[string]string{
				ManagedHCLabelKeysAnnotation: string(managedKeys),
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: nodePoolName,
						"old-key":                 "old-value",
						"staying-key":             "staying-value",
					},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithObjects(vm).
		Build()

	err := PropagateLabelsToInfraResources(context.Background(), cl, infraNS, nodePoolName, infraID, map[string]string{
		"staying-key": "staying-value",
	})
	g.Expect(err).ToNot(HaveOccurred())
	updated := &kubevirtv1.VirtualMachine{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Namespace: infraNS, Name: "vm-1"}, updated)).To(Succeed())
	g.Expect(updated.Spec.Template.ObjectMeta.Labels).To(HaveKeyWithValue("staying-key", "staying-value"))
	g.Expect(updated.Spec.Template.ObjectMeta.Labels).ToNot(HaveKey("old-key"), "stale key removed from HC labels should also be removed from the VMI template labels")
	g.Expect(updated.Spec.Template.ObjectMeta.Labels).To(HaveKeyWithValue(hyperv1.NodePoolNameLabel, nodePoolName))
}

func TestLabelsUpToDate(t *testing.T) {
	tests := []struct {
		name           string
		current        map[string]string
		desired        map[string]string
		expectUpToDate bool
	}{
		{
			name:           "When all desired labels are present with correct values, it should return true",
			current:        map[string]string{"env": "prod", "team": "platform"},
			desired:        map[string]string{"env": "prod", "team": "platform"},
			expectUpToDate: true,
		},
		{
			name:           "When a desired label is missing, it should return false",
			current:        map[string]string{"env": "prod"},
			desired:        map[string]string{"env": "prod", "team": "platform"},
			expectUpToDate: false,
		},
		{
			name:           "When a desired label has a different value, it should return false",
			current:        map[string]string{"env": "staging"},
			desired:        map[string]string{"env": "prod"},
			expectUpToDate: false,
		},
		{
			name:           "When desired labels are empty, it should return true",
			current:        map[string]string{"env": "prod"},
			desired:        map[string]string{},
			expectUpToDate: true,
		},
		{
			name:           "When desired labels are nil, it should return true",
			current:        map[string]string{"env": "prod"},
			desired:        nil,
			expectUpToDate: true,
		},
		{
			name:           "When current has extra labels not in desired, it should still return true",
			current:        map[string]string{"env": "prod", "extra": "value"},
			desired:        map[string]string{"env": "prod"},
			expectUpToDate: true,
		},
		{
			name:           "When desired contains reserved keys, they should be ignored",
			current:        map[string]string{},
			desired:        map[string]string{hyperv1.NodePoolNameLabel: "pool-1", hyperv1.InfraIDLabel: "id-1"},
			expectUpToDate: true,
		},
		{
			name:           "When desired has both reserved and non-reserved keys, only non-reserved are checked",
			current:        map[string]string{"env": "prod"},
			desired:        map[string]string{hyperv1.NodePoolNameLabel: "pool-1", "env": "prod"},
			expectUpToDate: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(LabelsUpToDate(tt.current, tt.desired)).To(Equal(tt.expectUpToDate))
		})
	}
}

func TestHashStabilityWithDifferentLabels(t *testing.T) {
	g := NewWithT(t)
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pool",
			Namespace: "clusters",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "test-cluster",
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: generateKubevirtPlatform(
					memoryNPOption("5Gi"),
					coresNPOption(4),
					imageNPOption("testimage"),
					volumeNPOption("32Gi"),
				),
			},
		},
	}
	hcluster1 := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "1234",
			Labels:  map[string]string{"label-a": "value-a"},
		},
	}
	hcluster2 := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "1234",
			Labels:  map[string]string{"label-b": "value-b"},
		},
	}
	bootImage := newBootImage("testimage", false)
	spec1, err := MachineTemplateSpec(nodePool, hcluster1, nil, bootImage, "")
	g.Expect(err).ToNot(HaveOccurred())
	spec2, err := MachineTemplateSpec(nodePool, hcluster2, nil, bootImage, "")
	g.Expect(err).ToNot(HaveOccurred())
	json1, _ := json.Marshal(spec1)
	json2, _ := json.Marshal(spec2)
	g.Expect(string(json1)).To(Equal(string(json2)), "Template specs with different hcluster.Spec.Labels should produce identical JSON (and thus identical hashes)")
}
