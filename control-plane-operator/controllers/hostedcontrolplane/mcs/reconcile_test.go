package mcs

import (
	"testing"

	. "github.com/onsi/gomega"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
)

func TestMasterConfigPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		verify func(g Gomega, pool *mcfgv1.MachineConfigPool)
	}{
		{
			name: "When called, it should return a pool named master",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Name).To(Equal("master"))
			},
		},
		{
			name: "When called, it should have the mco-built-in label",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/mco-built-in", ""))
			},
		},
		{
			name: "When called, it should select worker machine configs",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.MachineConfigSelector).NotTo(BeNil())
				g.Expect(pool.Spec.MachineConfigSelector.MatchLabels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			},
		},
		{
			name: "When called, it should select worker nodes",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.NodeSelector).NotTo(BeNil())
				g.Expect(pool.Spec.NodeSelector.MatchLabels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", ""))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			pool := masterConfigPool()
			tt.verify(g, pool)
		})
	}
}

func TestWorkerConfigPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		verify func(g Gomega, pool *mcfgv1.MachineConfigPool)
	}{
		{
			name: "When called, it should return a pool named worker",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Name).To(Equal("worker"))
			},
		},
		{
			name: "When called, it should have the mco-built-in label",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/mco-built-in", ""))
			},
		},
		{
			name: "When called, it should select worker machine configs",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.MachineConfigSelector).NotTo(BeNil())
				g.Expect(pool.Spec.MachineConfigSelector.MatchLabels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			},
		},
		{
			name: "When called, it should select worker nodes",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.NodeSelector).NotTo(BeNil())
				g.Expect(pool.Spec.NodeSelector.MatchLabels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", ""))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			pool := workerConfigPool()
			tt.verify(g, pool)
		})
	}
}
