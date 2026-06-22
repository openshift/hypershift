package nodepool

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
)

type mockProvider struct {
	info *instancetype.InstanceTypeInfo
	err  error
}

func (m *mockProvider) GetInstanceTypeInfo(_ context.Context, _ string) (*instancetype.InstanceTypeInfo, error) {
	return m.info, m.err
}

func TestTaintsToAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		taints   []hyperv1.Taint
		expected string
	}{
		{
			name:     "When taints are empty it should return empty string",
			taints:   []hyperv1.Taint{},
			expected: "",
		},
		{
			name: "When single taint it should format correctly",
			taints: []hyperv1.Taint{
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "dedicated=gpu:NoSchedule",
		},
		{
			name: "When single taint with empty value it should format as key:Effect",
			taints: []hyperv1.Taint{
				{Key: "node-role.kubernetes.io/infra", Value: "", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "node-role.kubernetes.io/infra:NoSchedule",
		},
		{
			name: "When multiple taints it should format and sort",
			taints: []hyperv1.Taint{
				{Key: "critical", Value: "true", Effect: corev1.TaintEffectNoExecute},
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "critical=true:NoExecute,dedicated=gpu:NoSchedule",
		},
		{
			name: "When taints with different effects it should format correctly",
			taints: []hyperv1.Taint{
				{Key: "node-role.kubernetes.io/infra", Value: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "workload", Value: "batch", Effect: corev1.TaintEffectPreferNoSchedule},
			},
			expected: "node-role.kubernetes.io/infra:NoSchedule,workload=batch:PreferNoSchedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := taintsToAnnotation(tt.taints)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestSetScaleFromZeroAnnotationsOnObject(t *testing.T) {
	newAWSTemplate := func(instanceType string) *infrav1.AWSMachineTemplate {
		return &infrav1.AWSMachineTemplate{
			Spec: infrav1.AWSMachineTemplateSpec{
				Template: infrav1.AWSMachineTemplateResource{
					Spec: infrav1.AWSMachineSpec{InstanceType: instanceType},
				},
			},
		}
	}

	tests := []struct {
		name            string
		provider        instancetype.Provider
		nodePool        *hyperv1.NodePool
		object          *capiv1.MachineDeployment
		machineTemplate interface{}
		expectErr       bool
		errSubstring    string
		validate        func(g Gomega, md *capiv1.MachineDeployment)
	}{
		{
			name:            "When machine template is an unsupported type it should return an error",
			provider:        &mockProvider{},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: "not-a-valid-template",
			expectErr:       true,
			errSubstring:    "unsupported machine template type",
		},
		{
			name:            "When instanceType is empty it should return an error",
			provider:        &mockProvider{},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate(""),
			expectErr:       true,
			errSubstring:    "instanceType is empty",
		},
		{
			name:            "When provider returns an error it should propagate the error",
			provider:        &mockProvider{err: fmt.Errorf("failed to describe instance type")},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       true,
			errSubstring:    "failed to describe instance type",
		},
		{
			name:     "When Status.Capacity is already provided it should remove scale-from-zero annotations",
			provider: &mockProvider{},
			nodePool: &hyperv1.NodePool{},
			object: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						cpuKey:           "4",
						memoryKey:        "16384",
						gpuKey:           "1",
						labelsKey:        "kubernetes.io/arch=amd64",
						taintsKey:        "dedicated=gpu:NoSchedule",
						"custom.io/keep": "preserved",
					},
				},
			},
			machineTemplate: &infrav1.AWSMachineTemplate{
				Spec: infrav1.AWSMachineTemplateSpec{
					Template: infrav1.AWSMachineTemplateResource{
						Spec: infrav1.AWSMachineSpec{InstanceType: "m5.large"},
					},
				},
				Status: infrav1.AWSMachineTemplateStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
				},
			},
			expectErr: false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				a := md.GetAnnotations()
				for _, k := range []string{cpuKey, memoryKey, gpuKey, labelsKey, taintsKey} {
					g.Expect(a).ToNot(HaveKey(k))
				}
				g.Expect(a).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			},
		},
		{
			name:            "When provider is nil it should return nil without setting annotations",
			provider:        nil,
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.GetAnnotations()).ToNot(HaveKey(cpuKey))
			},
		},
		{
			name: "When instance has no GPU and no taints it should set basic annotations and remove stale ones",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 2, MemoryMb: 8192, GPU: 0, CPUArchitecture: "amd64",
			}},
			nodePool: &hyperv1.NodePool{},
			object: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						gpuKey:    "2",
						taintsKey: "old=stale:NoSchedule",
					},
				},
			},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				a := md.GetAnnotations()
				g.Expect(a).To(HaveKeyWithValue(cpuKey, "2"))
				g.Expect(a).To(HaveKeyWithValue(memoryKey, "8192"))
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64"))
				g.Expect(a).ToNot(HaveKey(gpuKey))
				g.Expect(a).ToNot(HaveKey(taintsKey))
			},
		},
		{
			name: "When instance has GPU, labels with arch override, taints, and existing annotations it should set all correctly",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 8, MemoryMb: 61440, GPU: 1, CPUArchitecture: "arm64",
			}},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					NodeLabels: map[string]string{
						"env":                "production",
						"kubernetes.io/arch": "amd64", // should be overridden to arm64
					},
					Taints: []hyperv1.Taint{
						{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			object: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"custom.io/keep": "preserved"},
				},
			},
			machineTemplate: newAWSTemplate("p3.2xlarge"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				a := md.GetAnnotations()
				g.Expect(a).To(HaveKeyWithValue(cpuKey, "8"))
				g.Expect(a).To(HaveKeyWithValue(memoryKey, "61440"))
				g.Expect(a).To(HaveKeyWithValue(gpuKey, "1"))
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "env=production,kubernetes.io/arch=arm64"))
				g.Expect(a).To(HaveKeyWithValue(taintsKey, "dedicated=gpu:NoSchedule"))
				g.Expect(a).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := setScaleFromZeroAnnotationsOnObject(context.Background(), tt.provider, tt.nodePool, tt.object, tt.machineTemplate)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstring))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validate != nil {
					tt.validate(g, tt.object)
				}
			}
		})
	}
}
