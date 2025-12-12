/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machinedeployment

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsclient"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// fakeInstanceTypesCache is a mock implementation of InstanceTypesCache for testing.
type fakeInstanceTypesCache struct {
	instanceTypes map[string]InstanceType
	err           error
	callCount     int
}

func (f *fakeInstanceTypesCache) GetInstanceType(ctx context.Context, awsClient awsclient.Client, region string, instanceType string) (InstanceType, error) {
	f.callCount++
	if f.err != nil {
		return InstanceType{}, f.err
	}
	if it, ok := f.instanceTypes[instanceType]; ok {
		return it, nil
	}
	return InstanceType{}, fmt.Errorf("instance type %q not found in region %s", instanceType, region)
}

func newFakeInstanceTypesCache(instanceTypes map[string]InstanceType) *fakeInstanceTypesCache {
	return &fakeInstanceTypesCache{
		instanceTypes: instanceTypes,
	}
}

// fakeRegionCache is a mock implementation of awsclient.RegionCache for testing.
type fakeRegionCache struct {
	regions []string
	err     error
}

func (f *fakeRegionCache) GetCachedDescribeRegions(ctx context.Context, cfg aws.Config) (*ec2.DescribeRegionsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	regions := make([]types.Region, len(f.regions))
	for i, r := range f.regions {
		name := r
		optedIn := "opted-in"
		regions[i] = types.Region{
			RegionName:  &name,
			OptInStatus: &optedIn,
		}
	}
	return &ec2.DescribeRegionsOutput{Regions: regions}, nil
}

func newFakeRegionCache(regions ...string) *fakeRegionCache {
	return &fakeRegionCache{regions: regions}
}

// machineDeployment creates a test MachineDeployment with sensible defaults.
func machineDeployment(name, namespace string, mods ...func(*clusterv1.MachineDeployment)) *clusterv1.MachineDeployment {
	md := &clusterv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				nodePoolAnnotation: "test-nodepool",
			},
		},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: "test-cluster",
			Template: clusterv1.MachineTemplateSpec{
				Spec: clusterv1.MachineSpec{
					InfrastructureRef: corev1.ObjectReference{
						Kind:      "AWSMachineTemplate",
						Name:      "test-template",
						Namespace: namespace,
					},
				},
			},
		},
	}
	for _, mod := range mods {
		mod(md)
	}
	return md
}

// withInfraRefKind sets the infrastructure reference kind.
func withInfraRefKind(kind string) func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		md.Spec.Template.Spec.InfrastructureRef.Kind = kind
	}
}

// withInfraRefName sets the infrastructure reference name.
func withInfraRefName(name string) func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		md.Spec.Template.Spec.InfrastructureRef.Name = name
	}
}

// withDeletionTimestamp sets a deletion timestamp and adds a finalizer.
func withDeletionTimestamp() func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		now := metav1.Now()
		md.DeletionTimestamp = &now
		// Add a finalizer to make the object valid for the fake client
		md.Finalizers = []string{"test-finalizer"}
	}
}

// withExistingAnnotations merges annotations into existing ones.
func withExistingAnnotations(annotations map[string]string) func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		if md.Annotations == nil {
			md.Annotations = make(map[string]string)
		}
		for k, v := range annotations {
			md.Annotations[k] = v
		}
	}
}

// withRegionAnnotation sets the region annotation.
func withRegionAnnotation(region string) func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		if md.Annotations == nil {
			md.Annotations = make(map[string]string)
		}
		md.Annotations["capa.infrastructure.cluster.x-k8s.io/region"] = region
	}
}

// awsMachineTemplate creates a test AWSMachineTemplate.
func awsMachineTemplate(name, namespace, instanceType string, mods ...func(*capiaws.AWSMachineTemplate)) *capiaws.AWSMachineTemplate {
	template := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					InstanceType: instanceType,
				},
			},
		},
	}
	for _, mod := range mods {
		mod(template)
	}
	return template
}

// withStatusCapacity sets Status.Capacity on the AWSMachineTemplate.
func withStatusCapacity(capacity corev1.ResourceList) func(*capiaws.AWSMachineTemplate) {
	return func(t *capiaws.AWSMachineTemplate) {
		t.Status.Capacity = capacity
	}
}

// controlPlaneNamespace creates a namespace with the control-plane label.
func controlPlaneNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				controlPlaneNamespaceLabel: "true",
			},
		},
	}
}

// regularNamespace creates a namespace without the control-plane label.
func regularNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func TestReconcile(t *testing.T) {
	defaultInstanceTypes := map[string]InstanceType{
		"m6i.xlarge": {
			InstanceType:    "m6i.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureAmd64,
		},
		"m6g.xlarge": {
			InstanceType:    "m6g.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureArm64,
		},
		"p3.2xlarge": {
			InstanceType:    "p3.2xlarge",
			VCPU:            8,
			MemoryMb:        61440,
			GPU:             1,
			CPUArchitecture: ArchitectureAmd64,
		},
	}

	tests := []struct {
		name                   string
		existingObjects        []client.Object
		instanceTypesCache     InstanceTypesCache
		request                reconcile.Request
		expectedResult         ctrl.Result
		expectedError          bool
		expectedAnnotations    map[string]string
		expectedAnnotationsNil bool
	}{
		{
			name: "When MachineDeployment not found it should return nil error",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "nonexistent", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
		},
		{
			name: "When infraRef.Kind is not AWSMachineTemplate it should skip",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withInfraRefKind("AzureMachineTemplate")),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
		},
		{
			name: "When infraRef.Name is empty it should skip",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withInfraRefName("")),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
		},
		{
			name: "When namespace is not control-plane it should skip",
			existingObjects: []client.Object{
				regularNamespace("regular-ns"),
				machineDeployment("test-md", "regular-ns"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "regular-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
		},
		{
			name: "When MachineDeployment has deletion timestamp it should skip",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withDeletionTimestamp()),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
		},
		{
			name: "When AWSMachineTemplate has Status.Capacity populated it should remove workaround annotations",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns",
					withRegionAnnotation("us-east-1"),
					withExistingAnnotations(map[string]string{
						cpuKey:    "4",
						memoryKey: "16384",
						gpuKey:    "0",
						labelsKey: "kubernetes.io/arch=amd64",
					})),
				awsMachineTemplate("test-template", "test-ns", "m6i.xlarge",
					withStatusCapacity(corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					})),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
			expectedAnnotations: map[string]string{
				nodePoolAnnotation: "test-nodepool",
				"capa.infrastructure.cluster.x-k8s.io/region": "us-east-1",
			},
		},
		{
			name: "When AWSMachineTemplate has no Status.Capacity it should set annotations from instance type info",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withRegionAnnotation("us-east-1")),
				awsMachineTemplate("test-template", "test-ns", "m6i.xlarge"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
			expectedAnnotations: map[string]string{
				nodePoolAnnotation: "test-nodepool",
				"capa.infrastructure.cluster.x-k8s.io/region": "us-east-1",
				cpuKey:    "4",
				memoryKey: "16384",
				gpuKey:    "0",
				labelsKey: "kubernetes.io/arch=amd64",
			},
		},
		{
			name: "When instance type not found it should not requeue",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withRegionAnnotation("us-east-1")),
				awsMachineTemplate("test-template", "test-ns", "unknown.xlarge"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false, // permanent error, no requeue
		},
		{
			name: "When instance type has GPU it should set GPU annotation",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withRegionAnnotation("us-east-1")),
				awsMachineTemplate("test-template", "test-ns", "p3.2xlarge"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
			expectedAnnotations: map[string]string{
				nodePoolAnnotation: "test-nodepool",
				"capa.infrastructure.cluster.x-k8s.io/region": "us-east-1",
				cpuKey:    "8",
				memoryKey: "61440",
				gpuKey:    "1",
				labelsKey: "kubernetes.io/arch=amd64",
			},
		},
		{
			name: "When instance type is arm64 it should set correct architecture label",
			existingObjects: []client.Object{
				controlPlaneNamespace("test-ns"),
				machineDeployment("test-md", "test-ns", withRegionAnnotation("us-east-1")),
				awsMachineTemplate("test-template", "test-ns", "m6g.xlarge"),
			},
			instanceTypesCache: newFakeInstanceTypesCache(defaultInstanceTypes),
			request:            reconcile.Request{NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}},
			expectedResult:     ctrl.Result{},
			expectedError:      false,
			expectedAnnotations: map[string]string{
				nodePoolAnnotation: "test-nodepool",
				"capa.infrastructure.cluster.x-k8s.io/region": "us-east-1",
				cpuKey:    "4",
				memoryKey: "16384",
				gpuKey:    "0",
				labelsKey: "kubernetes.io/arch=arm64",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tt.existingObjects...).
				Build()

			reconciler := &Reconciler{
				Client:             fakeClient,
				InstanceTypesCache: tt.instanceTypesCache,
				RegionCache:        newFakeRegionCache("us-east-1", "us-west-2"),
				recorder:           record.NewFakeRecorder(10),
				scheme:             api.Scheme,
			}

			result, err := reconciler.Reconcile(context.Background(), tt.request)

			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(tt.expectedResult))

			if tt.expectedAnnotations != nil {
				// Fetch the updated MachineDeployment
				md := &clusterv1.MachineDeployment{}
				err := fakeClient.Get(context.Background(), tt.request.NamespacedName, md)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(md.Annotations).To(testutil.MatchExpected(tt.expectedAnnotations))
			}
		})
	}
}

func TestReconcile_WhenPreservingExistingLabels_ItShouldMergeWithArchitecture(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := map[string]InstanceType{
		"m6i.xlarge": {
			InstanceType:    "m6i.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureAmd64,
		},
	}

	// Create MachineDeployment with existing custom labels
	md := machineDeployment("test-md", "test-ns",
		withRegionAnnotation("us-east-1"),
		withExistingAnnotations(map[string]string{
			labelsKey: "custom.label/foo=bar,custom.label/baz=qux",
		}))

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(
			controlPlaneNamespace("test-ns"),
			md,
			awsMachineTemplate("test-template", "test-ns", "m6i.xlarge"),
		).
		Build()

	reconciler := &Reconciler{
		Client:             fakeClient,
		InstanceTypesCache: newFakeInstanceTypesCache(instanceTypes),
		RegionCache:        newFakeRegionCache("us-east-1"),
		recorder:           record.NewFakeRecorder(10),
		scheme:             api.Scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())

	// Fetch the updated MachineDeployment
	updatedMD := &clusterv1.MachineDeployment{}
	err = fakeClient.Get(context.Background(), k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}, updatedMD)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify architecture label is added while preserving custom labels
	labelsAnnotation := updatedMD.Annotations[labelsKey]
	g.Expect(labelsAnnotation).To(ContainSubstring("kubernetes.io/arch=amd64"))
	g.Expect(labelsAnnotation).To(ContainSubstring("custom.label/foo=bar"))
	g.Expect(labelsAnnotation).To(ContainSubstring("custom.label/baz=qux"))
}

func TestHasNodePoolAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		expected bool
	}{
		{
			name: "When object has nodePool annotation it should return true",
			obj: &clusterv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						nodePoolAnnotation: "test-nodepool",
					},
				},
			},
			expected: true,
		},
		{
			name: "When object has no annotations it should return false",
			obj: &clusterv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expected: false,
		},
		{
			name: "When object has annotations but not nodePool it should return false",
			obj: &clusterv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := &Reconciler{}
			result := r.hasNodePoolAnnotation(tt.obj)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

// nodePool creates a test NodePool with sensible defaults.
func nodePool(name, namespace string, mods ...func(*hyperv1.NodePool)) *hyperv1.NodePool {
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "test-cluster",
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
	for _, mod := range mods {
		mod(np)
	}
	return np
}

// withNodeLabels sets NodeLabels on the NodePool.
func withNodeLabels(labels map[string]string) func(*hyperv1.NodePool) {
	return func(np *hyperv1.NodePool) {
		np.Spec.NodeLabels = labels
	}
}

// withTaints sets Taints on the NodePool.
func withTaints(taints []hyperv1.Taint) func(*hyperv1.NodePool) {
	return func(np *hyperv1.NodePool) {
		np.Spec.Taints = taints
	}
}

// withNodePoolAnnotation sets the nodePool annotation to reference a specific NodePool.
func withNodePoolAnnotation(namespace, name string) func(*clusterv1.MachineDeployment) {
	return func(md *clusterv1.MachineDeployment) {
		if md.Annotations == nil {
			md.Annotations = make(map[string]string)
		}
		md.Annotations[nodePoolAnnotation] = fmt.Sprintf("%s/%s", namespace, name)
	}
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
			expected: "node-role.kubernetes.io/infra=:NoSchedule,workload=batch:PreferNoSchedule",
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

func TestReconcile_WhenNodePoolHasTaints_ItShouldSetTaintsAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := map[string]InstanceType{
		"m6i.xlarge": {
			InstanceType:    "m6i.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureAmd64,
		},
	}

	nodePoolObj := nodePool("test-nodepool", "default",
		withTaints([]hyperv1.Taint{
			{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			{Key: "critical", Value: "true", Effect: corev1.TaintEffectNoExecute},
		}))

	md := machineDeployment("test-md", "test-ns",
		withRegionAnnotation("us-east-1"),
		withNodePoolAnnotation("default", "test-nodepool"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(
			controlPlaneNamespace("test-ns"),
			md,
			nodePoolObj,
			awsMachineTemplate("test-template", "test-ns", "m6i.xlarge"),
		).
		Build()

	reconciler := &Reconciler{
		Client:             fakeClient,
		InstanceTypesCache: newFakeInstanceTypesCache(instanceTypes),
		RegionCache:        newFakeRegionCache("us-east-1"),
		recorder:           record.NewFakeRecorder(10),
		scheme:             api.Scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())

	// Fetch the updated MachineDeployment
	updatedMD := &clusterv1.MachineDeployment{}
	err = fakeClient.Get(context.Background(), k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}, updatedMD)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify taints annotation is set correctly (sorted)
	g.Expect(updatedMD.Annotations[taintsKey]).To(Equal("critical=true:NoExecute,dedicated=gpu:NoSchedule"))
}

func TestReconcile_WhenNodePoolHasNoTaints_ItShouldNotSetTaintsAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := map[string]InstanceType{
		"m6i.xlarge": {
			InstanceType:    "m6i.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureAmd64,
		},
	}

	nodePoolObj := nodePool("test-nodepool", "default")

	md := machineDeployment("test-md", "test-ns",
		withRegionAnnotation("us-east-1"),
		withNodePoolAnnotation("default", "test-nodepool"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(
			controlPlaneNamespace("test-ns"),
			md,
			nodePoolObj,
			awsMachineTemplate("test-template", "test-ns", "m6i.xlarge"),
		).
		Build()

	reconciler := &Reconciler{
		Client:             fakeClient,
		InstanceTypesCache: newFakeInstanceTypesCache(instanceTypes),
		RegionCache:        newFakeRegionCache("us-east-1"),
		recorder:           record.NewFakeRecorder(10),
		scheme:             api.Scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())

	// Fetch the updated MachineDeployment
	updatedMD := &clusterv1.MachineDeployment{}
	err = fakeClient.Get(context.Background(), k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}, updatedMD)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify taints annotation is not set
	_, exists := updatedMD.Annotations[taintsKey]
	g.Expect(exists).To(BeFalse())
}

func TestReconcile_WhenNodePoolHasNodeLabels_ItShouldIncludeInLabelsAnnotation(t *testing.T) {
	g := NewGomegaWithT(t)

	instanceTypes := map[string]InstanceType{
		"m6i.xlarge": {
			InstanceType:    "m6i.xlarge",
			VCPU:            4,
			MemoryMb:        16384,
			GPU:             0,
			CPUArchitecture: ArchitectureAmd64,
		},
	}

	nodePoolObj := nodePool("test-nodepool", "default",
		withNodeLabels(map[string]string{
			"node.kubernetes.io/instance-type": "m6i.xlarge",
			"workload-type":                    "compute",
		}))

	md := machineDeployment("test-md", "test-ns",
		withRegionAnnotation("us-east-1"),
		withNodePoolAnnotation("default", "test-nodepool"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(
			controlPlaneNamespace("test-ns"),
			md,
			nodePoolObj,
			awsMachineTemplate("test-template", "test-ns", "m6i.xlarge"),
		).
		Build()

	reconciler := &Reconciler{
		Client:             fakeClient,
		InstanceTypesCache: newFakeInstanceTypesCache(instanceTypes),
		RegionCache:        newFakeRegionCache("us-east-1"),
		recorder:           record.NewFakeRecorder(10),
		scheme:             api.Scheme,
	}

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())

	// Fetch the updated MachineDeployment
	updatedMD := &clusterv1.MachineDeployment{}
	err = fakeClient.Get(context.Background(), k8stypes.NamespacedName{Name: "test-md", Namespace: "test-ns"}, updatedMD)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify labels annotation includes both architecture and NodePool labels
	labelsAnnotation := updatedMD.Annotations[labelsKey]
	g.Expect(labelsAnnotation).To(ContainSubstring("kubernetes.io/arch=amd64"))
	g.Expect(labelsAnnotation).To(ContainSubstring("node.kubernetes.io/instance-type=m6i.xlarge"))
	g.Expect(labelsAnnotation).To(ContainSubstring("workload-type=compute"))
}
