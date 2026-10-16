package nodepool

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockProvider struct {
	info *instancetype.InstanceTypeInfo
	err  error
}

func (m *mockProvider) GetInstanceTypeInfo(_ context.Context, _ string) (*instancetype.InstanceTypeInfo, error) {
	return m.info, m.err
}

type schedulingMetadataClient struct {
	client.Client
	getErr   error
	patchErr error
}

func (c *schedulingMetadataClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.getErr != nil {
		return c.getErr
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *schedulingMetadataClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestReconcileScaleFromZeroAnnotations(t *testing.T) {
	tests := []struct {
		name             string
		upgradeType      hyperv1.UpgradeType
		platform         hyperv1.PlatformType
		disabled         bool
		missingTemplate  bool
		noNativeCapacity bool
		getErr, patchErr error
		provider         instancetype.Provider
		providerPlatform hyperv1.PlatformType
		expectedError    string
	}{
		{name: "When Replace uses native capacity without a legacy provider, it should reconcile NodePool labels and taints", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform},
		{name: "When InPlace uses native capacity without a legacy provider, it should reconcile NodePool labels and taints", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AWSPlatform},
		{name: "When Azure has native capacity, it should reconcile NodePool labels and taints", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AzurePlatform},
		{name: "When a different platform's provider is configured without native capacity, it should not query that provider", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AzurePlatform, noNativeCapacity: true, providerPlatform: hyperv1.AWSPlatform, provider: &mockProvider{err: fmt.Errorf("wrong provider must not be queried")}},
		{name: "When the AWS provider is removed without native capacity, it should clear stale capacity and preserve scheduling metadata", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, noNativeCapacity: true},
		{name: "When the Azure provider is removed for InPlace without native capacity, it should clear stale capacity and preserve scheduling metadata", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AzurePlatform, noNativeCapacity: true},
		{name: "When autoscaling is disabled, it should leave metadata untouched", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, disabled: true},
		{name: "When the platform is unsupported, it should leave metadata untouched", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.OpenStackPlatform},
		{name: "When the machine template is not yet present, it should wait without modifying metadata", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, missingTemplate: true},
		{name: "When reading CAPI resources fails, it should return a retryable error", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, getErr: fmt.Errorf("read unavailable"), expectedError: "read unavailable"},
		{name: "When patching scheduling metadata fails, it should return a retryable error", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AWSPlatform, patchErr: fmt.Errorf("write conflict"), expectedError: "write conflict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			np := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
				Spec: hyperv1.NodePoolSpec{
					Arch: "arm64", Platform: hyperv1.NodePoolPlatform{Type: tc.platform},
					AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To[int32](0), Max: 1},
					Management:  hyperv1.NodePoolManagement{UpgradeType: tc.upgradeType},
					NodeLabels:  map[string]string{"workload": "workload", "topology.kubernetes.io/zone": "eu-central-1b"},
					Taints:      []hyperv1.Taint{{Key: "dedicated", Value: "workload", Effect: corev1.TaintEffectNoSchedule}},
				},
			}
			if tc.disabled {
				np.Spec.AutoScaling = nil
			}
			metadata := metav1.ObjectMeta{Name: "workers-template", Namespace: "clusters-zone-check"}
			capacity := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")}
			if tc.noNativeCapacity {
				capacity = nil
			}
			var template client.Object = &infrav1.AWSMachineTemplate{ObjectMeta: metadata, Status: infrav1.AWSMachineTemplateStatus{Capacity: capacity}}
			if tc.platform == hyperv1.AzurePlatform {
				template = &capiazure.AzureMachineTemplate{ObjectMeta: metadata, Status: capiazure.AzureMachineTemplateStatus{Capacity: capacity}}
			}
			objectMeta := metav1.ObjectMeta{Name: np.Name, Namespace: metadata.Namespace, Annotations: map[string]string{
				labelsKey: "stale=old", taintsKey: "stale=old:NoSchedule", cpuKey: "1", memoryKey: "1024", gpuKey: "2", "custom.io/keep": "preserved",
			}}
			machineTemplate := capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
				Version: "4.22.15", InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: template.GetName()},
				Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To("workers-existing-user-data")},
			}}
			var object client.Object = &capiv1.MachineDeployment{ObjectMeta: objectMeta, Spec: capiv1.MachineDeploymentSpec{Template: machineTemplate, Replicas: ptr.To[int32](0)}}
			if tc.upgradeType == hyperv1.UpgradeTypeInPlace {
				object = &capiv1.MachineSet{ObjectMeta: objectMeta, Spec: capiv1.MachineSetSpec{Template: machineTemplate, Replicas: ptr.To[int32](0)}}
			}
			objects := []client.Object{object}
			if !tc.missingTemplate {
				objects = append(objects, template)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			capi := &CAPI{Token: &Token{ConfigGenerator: &ConfigGenerator{
				Client: &schedulingMetadataClient{Client: c, getErr: tc.getErr, patchErr: tc.patchErr}, nodePool: np, controlplaneNamespace: metadata.Namespace,
			}}}
			r := &NodePoolReconciler{InstanceTypeProvider: tc.provider, ScaleFromZeroPlatform: tc.providerPlatform}
			err := r.reconcileScaleFromZeroAnnotations(t.Context(), np, capi)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			actual := object.DeepCopyObject().(client.Object)
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			if tc.disabled || tc.platform == hyperv1.OpenStackPlatform || tc.missingTemplate || tc.expectedError != "" {
				g.Expect(actual.GetAnnotations()).To(Equal(object.GetAnnotations()))
				return
			}
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,topology.kubernetes.io/zone=eu-central-1b,workload=workload"))
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(taintsKey, "dedicated=workload:NoSchedule"))
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			for _, key := range []string{cpuKey, memoryKey, gpuKey} {
				g.Expect(actual.GetAnnotations()).ToNot(HaveKey(key), "stale capacity must be removed when native capacity is available or no provider remains")
			}
			switch actual := actual.(type) {
			case *capiv1.MachineDeployment:
				g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineDeployment).Spec))
			case *capiv1.MachineSet:
				g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineSet).Spec))
			}
			resourceVersion := actual.GetResourceVersion()
			g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), np, capi)).To(Succeed())
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			g.Expect(actual.GetResourceVersion()).To(Equal(resourceVersion), "unchanged metadata must not cause another write")
			np.Spec.NodeLabels = map[string]string{"updated": "true"}
			np.Spec.Taints = nil
			g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), np, capi)).To(Succeed())
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,updated=true"))
			g.Expect(actual.GetAnnotations()).ToNot(HaveKey(taintsKey))
		})
	}
}

func TestTaintsToAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		taints   []hyperv1.Taint
		expected string
	}{
		{
			name:     "When taints are empty, it should return empty string",
			taints:   []hyperv1.Taint{},
			expected: "",
		},
		{
			name: "When single taint, it should format correctly",
			taints: []hyperv1.Taint{
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "dedicated=gpu:NoSchedule",
		},
		{
			name: "When single taint with empty value, it should format as key:Effect",
			taints: []hyperv1.Taint{
				{Key: "node-role.kubernetes.io/infra", Value: "", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "node-role.kubernetes.io/infra:NoSchedule",
		},
		{
			name: "When multiple taints, it should format and sort",
			taints: []hyperv1.Taint{
				{Key: "critical", Value: "true", Effect: corev1.TaintEffectNoExecute},
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "critical=true:NoExecute,dedicated=gpu:NoSchedule",
		},
		{
			name: "When taints with different effects, it should format correctly",
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
	providerErr := errors.New("failed to describe instance type")
	newAWSTemplate := func(instanceType string) *infrav1.AWSMachineTemplate {
		return &infrav1.AWSMachineTemplate{
			Spec: infrav1.AWSMachineTemplateSpec{
				Template: infrav1.AWSMachineTemplateResource{
					Spec: infrav1.AWSMachineSpec{InstanceType: instanceType},
				},
			},
		}
	}

	newAzureTemplate := func(vmSize string) *capiazure.AzureMachineTemplate {
		return &capiazure.AzureMachineTemplate{
			Spec: capiazure.AzureMachineTemplateSpec{
				Template: capiazure.AzureMachineTemplateResource{
					Spec: capiazure.AzureMachineSpec{VMSize: vmSize},
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
		errCause        error
		validate        func(g Gomega, md *capiv1.MachineDeployment)
	}{
		{
			name: "When all zonal and custom labels are supplied on the NodePool with native capacity, it should advertise them without discovering AWS topology",
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
				Arch: "amd64",
				NodeLabels: map[string]string{
					"failure-domain.beta.kubernetes.io/region": "eu-central-1",
					"failure-domain.beta.kubernetes.io/zone":   "eu-central-1b",
					"topology.kubernetes.io/region":            "eu-central-1",
					"topology.kubernetes.io/zone":              "eu-central-1b",
					"topology.ebs.csi.aws.com/zone":            "eu-central-1b",
					"topology.k8s.aws/zone-id":                 "euc1-az3",
					"workload":                                 "workload",
				},
			}},
			object: &capiv1.MachineDeployment{},
			machineTemplate: &infrav1.AWSMachineTemplate{Status: infrav1.AWSMachineTemplateStatus{
				Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("96"), corev1.ResourceMemory: resource.MustParse("192Gi")},
			}},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				nodeLabels, err := labels.ConvertSelectorToLabelsMap(md.Annotations[labelsKey])
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(nodeLabels).To(Equal(labels.Set{
					"kubernetes.io/arch":                       "amd64",
					"failure-domain.beta.kubernetes.io/region": "eu-central-1",
					"failure-domain.beta.kubernetes.io/zone":   "eu-central-1b",
					"topology.kubernetes.io/region":            "eu-central-1",
					"topology.kubernetes.io/zone":              "eu-central-1b",
					"topology.ebs.csi.aws.com/zone":            "eu-central-1b",
					"topology.k8s.aws/zone-id":                 "euc1-az3",
					"workload":                                 "workload",
				}))
			},
		},
		{
			name:     "When Azure supplies native node architecture, it should keep labels and taints without querying a legacy provider",
			provider: &mockProvider{err: fmt.Errorf("provider must not be queried")},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
				Arch: "amd64", NodeLabels: map[string]string{"workload": "workload", "kubernetes.io/arch": "amd64"},
				Taints: []hyperv1.Taint{{Key: "dedicated", Value: "workload", Effect: corev1.TaintEffectNoSchedule}},
			}},
			object: &capiv1.MachineDeployment{},
			machineTemplate: &capiazure.AzureMachineTemplate{Status: capiazure.AzureMachineTemplateStatus{
				Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")},
				NodeInfo: &capiazure.NodeInfo{Architecture: capiazure.ArchitectureArm64},
			}},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,workload=workload"))
				g.Expect(md.Annotations).To(HaveKeyWithValue(taintsKey, "dedicated=workload:NoSchedule"))
			},
		},
		{
			name:     "When AWS supplies native node architecture, it should prefer that architecture without querying a legacy provider",
			provider: &mockProvider{err: fmt.Errorf("provider must not be queried")},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Arch: "amd64"}},
			object:   &capiv1.MachineDeployment{},
			machineTemplate: &infrav1.AWSMachineTemplate{Status: infrav1.AWSMachineTemplateStatus{
				Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi")},
				NodeInfo: &infrav1.NodeInfo{Architecture: infrav1.ArchitectureArm64},
			}},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64"))
			},
		},
		{
			name:            "When machine template is an unsupported type, it should return an error",
			provider:        &mockProvider{},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: "not-a-valid-template",
			expectErr:       true,
			errSubstring:    "unsupported machine template type",
		},
		{
			name:            "When instanceType is empty, it should return an error",
			provider:        &mockProvider{},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate(""),
			expectErr:       true,
			errSubstring:    "instanceType is empty",
		},
		{
			name:            "When provider returns an error, it should propagate the error",
			provider:        &mockProvider{err: providerErr},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       true,
			errSubstring:    `failed to get instance type information for "m5.large": failed to describe instance type`,
			errCause:        providerErr,
		},
		{
			name: "When provider omits architecture, it should preserve the NodePool architecture",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 2, MemoryMb: 8192,
			}},
			nodePool:        &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Arch: "arm64"}},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64"))
			},
		},
		{
			name: "When provider omits architecture, it should preserve the machine template architecture",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 2, MemoryMb: 8192,
			}},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Arch: "amd64"}},
			object:   &capiv1.MachineDeployment{},
			machineTemplate: &infrav1.AWSMachineTemplate{
				Spec: infrav1.AWSMachineTemplateSpec{
					Template: infrav1.AWSMachineTemplateResource{
						Spec: infrav1.AWSMachineSpec{InstanceType: "m5.large"},
					},
				},
				Status: infrav1.AWSMachineTemplateStatus{
					NodeInfo: &infrav1.NodeInfo{Architecture: infrav1.ArchitectureArm64},
				},
			},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64"))
			},
		},
		{
			name:     "When native capacity is available, it should reconcile NodePool labels and taints without querying the capacity provider",
			provider: &mockProvider{err: fmt.Errorf("capacity provider must not be queried")},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
				Arch: "amd64",
				NodeLabels: map[string]string{
					"topology.kubernetes.io/zone": "eu-central-1b",
					"workload":                    "workload",
				},
				Taints: []hyperv1.Taint{{Key: "dedicated", Value: "workload", Effect: corev1.TaintEffectNoSchedule}},
			}},
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
				for _, k := range []string{cpuKey, memoryKey, gpuKey} {
					g.Expect(a).ToNot(HaveKey(k))
				}
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64,topology.kubernetes.io/zone=eu-central-1b,workload=workload"))
				g.Expect(a).To(HaveKeyWithValue(taintsKey, "dedicated=workload:NoSchedule"))
				g.Expect(a).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			},
		},
		{
			name:     "When provider is nil, it should set scheduling metadata without adding capacity annotations",
			provider: nil,
			nodePool: &hyperv1.NodePool{},
			object: &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				cpuKey: "4", memoryKey: "16384", gpuKey: "1", "custom.io/keep": "preserved",
			}}},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				for _, key := range []string{cpuKey, memoryKey, gpuKey} {
					g.Expect(md.GetAnnotations()).ToNot(HaveKey(key), "capacity from a removed provider must not linger")
				}
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue("custom.io/keep", "preserved"), "unrelated annotations must survive cleanup")
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64"))
			},
		},
		{
			name: "When instance has no GPU and no taints, it should set basic annotations and remove stale ones",
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
			name: "When Azure template with valid VMSize and no GPU, it should set basic annotations",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 4, MemoryMb: 16384, GPU: 0, CPUArchitecture: "amd64",
			}},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAzureTemplate("Standard_D4s_v5"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				a := md.GetAnnotations()
				g.Expect(a).To(HaveKeyWithValue(cpuKey, "4"))
				g.Expect(a).To(HaveKeyWithValue(memoryKey, "16384"))
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64"))
				g.Expect(a).ToNot(HaveKey(gpuKey))
			},
		},
		{
			name:            "When Azure template with empty VMSize, it should return error",
			provider:        &mockProvider{},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAzureTemplate(""),
			expectErr:       true,
			errSubstring:    "instanceType is empty",
		},
		{
			name:     "When Azure has no legacy provider, it should set scheduling metadata without adding capacity annotations",
			provider: nil,
			nodePool: &hyperv1.NodePool{},
			object: &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				cpuKey: "4", memoryKey: "16384", gpuKey: "1", "custom.io/keep": "preserved",
			}}},
			machineTemplate: newAzureTemplate("Standard_D4s_v5"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				for _, key := range []string{cpuKey, memoryKey, gpuKey} {
					g.Expect(md.GetAnnotations()).ToNot(HaveKey(key), "capacity from a removed provider must not linger")
				}
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue("custom.io/keep", "preserved"), "unrelated annotations must survive cleanup")
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64"))
			},
		},
		{
			name: "When Azure template with GPU and taints, it should set all annotations",
			provider: &mockProvider{info: &instancetype.InstanceTypeInfo{
				VCPU: 6, MemoryMb: 114688, GPU: 1, CPUArchitecture: "amd64",
			}},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Taints: []hyperv1.Taint{
						{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAzureTemplate("Standard_NC6s_v3"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				a := md.GetAnnotations()
				g.Expect(a).To(HaveKeyWithValue(cpuKey, "6"))
				g.Expect(a).To(HaveKeyWithValue(memoryKey, "114688"))
				g.Expect(a).To(HaveKeyWithValue(gpuKey, "1"))
				g.Expect(a).To(HaveKeyWithValue(taintsKey, "dedicated=gpu:NoSchedule"))
			},
		},
		{
			name: "When instance has GPU, labels with arch override, taints, and existing annotations, it should set all correctly",
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
			original := tt.object.DeepCopy()
			err := setScaleFromZeroAnnotationsOnObject(t.Context(), tt.provider, tt.nodePool, tt.object, tt.machineTemplate)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstring), "errors must identify the failed operation")
				if tt.errCause != nil {
					g.Expect(errors.Is(err, tt.errCause)).To(BeTrue(), "provider errors must remain inspectable through wrapping")
				}
				g.Expect(tt.object.Annotations).To(Equal(original.Annotations))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.validate != nil {
					tt.validate(g, tt.object)
				}
			}
		})
	}
}
