package nodepool

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/awsmachinetopology"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/autoscaling"
	"github.com/openshift/hypershift/support/releaseinfo"

	imageapi "github.com/openshift/api/image/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockProvider struct {
	info *instancetype.InstanceTypeInfo
	err  error
}

type scaleFromZeroClient struct {
	client.Client
	getErr   error
	patchErr error
}

func (c *scaleFromZeroClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.getErr != nil {
		return c.getErr
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *scaleFromZeroClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

type scaleFromZeroEC2Client struct {
	zone   string
	zoneID string
	err    error
}

func (c *scaleFromZeroEC2Client) DescribeSubnets(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if c.err != nil {
		return nil, c.err
	}
	zone, zoneID := c.zone, c.zoneID
	if zone == "" {
		zone, zoneID = "eu-central-1b", "euc1-az3"
	}
	return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{
		SubnetId: aws.String("subnet-0a1b2c3d4e5f67890"), AvailabilityZone: aws.String(zone), AvailabilityZoneId: aws.String(zoneID),
	}}}, nil
}

func TestReconcileScaleFromZeroAnnotations(t *testing.T) {
	t.Run("When autoscaling is disabled, it should leave scheduling metadata untouched", func(t *testing.T) {
		g := NewWithT(t)
		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
			Spec: hyperv1.NodePoolSpec{
				Arch:       "arm64",
				Platform:   hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform},
				Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
			},
		}
		template := &infrav1.AWSMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "workers-template", Namespace: "clusters-zone-check"},
			Status: infrav1.AWSMachineTemplateStatus{
				Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
			},
		}
		md := &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodePool.Name,
				Namespace: template.Namespace,
				Annotations: map[string]string{
					labelsKey: "custom.io/existing=preserved",
				},
			},
			Spec: capiv1.MachineDeploymentSpec{
				Template: capiv1.MachineTemplateSpec{
					Spec: capiv1.MachineSpec{
						InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: template.Name},
					},
				},
			},
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(template, md).Build()
		capi := &CAPI{Token: &Token{ConfigGenerator: &ConfigGenerator{
			Client: c, nodePool: nodePool, controlplaneNamespace: template.Namespace,
		}}}
		r := &NodePoolReconciler{}
		g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)).To(Succeed())
		actual := &capiv1.MachineDeployment{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(md), actual)).To(Succeed())
		g.Expect(actual.Annotations).To(Equal(md.Annotations))
	})

	tests := []struct {
		name             string
		upgradeType      hyperv1.UpgradeType
		platform         hyperv1.PlatformType
		nativeCapacity   bool
		discoverTopology bool
		provider         instancetype.Provider
		providerPlatform hyperv1.PlatformType
		missingTemplate  bool
		missingScalable  bool
		getErr           error
		patchErr         error
		expectedError    string
	}{
		{name: "When an AWS Replace pool has native capacity and no legacy provider, it should consume all CPO topology labels", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, nativeCapacity: true, discoverTopology: true},
		{name: "When an AWS InPlace pool has native capacity and no legacy provider, it should consume all CPO topology labels", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AWSPlatform, nativeCapacity: true, discoverTopology: true},
		{name: "When Azure has native capacity and no legacy provider, it should reconcile labels and taints without AWS lookups", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AzurePlatform, nativeCapacity: true},
		{name: "When a matching legacy provider is configured, it should preserve resource capacity fallback", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, providerPlatform: hyperv1.AWSPlatform, provider: &mockProvider{info: &instancetype.InstanceTypeInfo{VCPU: 4, MemoryMb: 16384, CPUArchitecture: "arm64"}}},
		{name: "When the legacy provider belongs to another platform, it should not query it", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AzurePlatform, providerPlatform: hyperv1.AWSPlatform, provider: &mockProvider{err: fmt.Errorf("wrong provider must not be queried")}},
		{name: "When the platform does not support these annotations, it should leave existing metadata untouched", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.OpenStackPlatform},
		{name: "When the machine template does not exist yet, it should wait without modifying metadata", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, missingTemplate: true},
		{name: "When the scalable resource does not exist yet, it should wait without an error", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AWSPlatform, missingScalable: true},
		{name: "When fetching the scalable resource fails, it should return a retryable error", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, getErr: fmt.Errorf("read unavailable"), expectedError: "read unavailable"},
		{name: "When patching scheduling metadata fails, it should return a retryable error", upgradeType: hyperv1.UpgradeTypeInPlace, platform: hyperv1.AWSPlatform, nativeCapacity: true, patchErr: fmt.Errorf("write conflict"), expectedError: "write conflict"},
		{name: "When the legacy provider fails, it should return an error without a partial patch", upgradeType: hyperv1.UpgradeTypeReplace, platform: hyperv1.AWSPlatform, providerPlatform: hyperv1.AWSPlatform, provider: &mockProvider{err: fmt.Errorf("capacity unavailable")}, expectedError: "capacity unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "zone-check", Arch: "arm64",
					AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To[int32](0), Max: 1},
					Platform:    hyperv1.NodePoolPlatform{Type: tc.platform},
					Management:  hyperv1.NodePoolManagement{UpgradeType: tc.upgradeType},
					NodeLabels:  map[string]string{"workload": "envoy", "kubernetes.io/arch": "amd64"},
					Taints:      []hyperv1.Taint{{Key: "dedicated", Value: "envoy", Effect: corev1.TaintEffectNoSchedule}},
				},
			}
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: nodePool.Spec.ClusterName, Namespace: "clusters-zone-check"},
				Spec:       hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "eu-central-1"}}},
			}
			capacity := corev1.ResourceList{}
			if tc.nativeCapacity {
				capacity[corev1.ResourceCPU] = resource.MustParse("4")
				capacity[corev1.ResourceMemory] = resource.MustParse("16Gi")
			}
			var template client.Object = &infrav1.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{Name: "workers-template", Namespace: hcp.Namespace, Annotations: map[string]string{hyperv1.NodePoolLabel: client.ObjectKeyFromObject(nodePool).String()}},
				Spec: infrav1.AWSMachineTemplateSpec{Template: infrav1.AWSMachineTemplateResource{Spec: infrav1.AWSMachineSpec{
					InstanceType: "m6g.xlarge", Subnet: &infrav1.AWSResourceReference{ID: aws.String("subnet-0a1b2c3d4e5f67890")},
				}}},
				Status: infrav1.AWSMachineTemplateStatus{Capacity: capacity},
			}
			if tc.platform == hyperv1.AzurePlatform {
				template = &capiazure.AzureMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{Name: "workers-template", Namespace: hcp.Namespace},
					Spec:       capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{VMSize: "Standard_D4ps_v5"}}},
					Status:     capiazure.AzureMachineTemplateStatus{Capacity: capacity},
				}
			}
			if tc.discoverTopology {
				// Filters can select a different subnet without changing this template spec.
				template.(*infrav1.AWSMachineTemplate).Spec.Template.Spec.Subnet = &infrav1.AWSResourceReference{
					Filters: []infrav1.Filter{{Name: "tag:Name", Values: []string{"workers-subnet"}}},
				}
			}
			metadata := metav1.ObjectMeta{Name: nodePool.Name, Namespace: hcp.Namespace, Annotations: map[string]string{
				labelsKey: "stale=old", taintsKey: "stale=old:NoSchedule", cpuKey: "1", memoryKey: "1024", gpuKey: "2", "custom.io/keep": "preserved",
			}}
			machineTemplate := capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
				Version: "4.22.15", InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: template.GetName()},
				Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To("workers-existing-user-data")},
			}}
			var object client.Object = &capiv1.MachineDeployment{ObjectMeta: metadata, Spec: capiv1.MachineDeploymentSpec{Replicas: ptr.To[int32](0), Template: machineTemplate}}
			if tc.upgradeType == hyperv1.UpgradeTypeInPlace {
				object = &capiv1.MachineSet{ObjectMeta: metadata, Spec: capiv1.MachineSetSpec{Replicas: ptr.To[int32](0), Template: machineTemplate}}
			}
			objects := []client.Object{hcp}
			if !tc.missingTemplate {
				objects = append(objects, template)
			}
			if !tc.missingScalable {
				objects = append(objects, object)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			var cpo *awsmachinetopology.Reconciler
			ec2Client := &scaleFromZeroEC2Client{}
			if tc.discoverTopology {
				cpo = &awsmachinetopology.Reconciler{Client: c, HostedControlPlane: client.ObjectKeyFromObject(hcp), EC2Client: ec2Client}
				_, err := cpo.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(template)})
				g.Expect(err).ToNot(HaveOccurred())
			}
			configGenerator := &ConfigGenerator{
				Client: &scaleFromZeroClient{Client: c, getErr: tc.getErr, patchErr: tc.patchErr}, nodePool: nodePool, controlplaneNamespace: hcp.Namespace,
				rolloutConfig: &rolloutConfig{mcoRawConfig: "existing-ignition", pullSecretName: "pull-secret", releaseImage: &releaseinfo.ReleaseImage{ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.22.15"}}}},
			}
			capi := &CAPI{Token: &Token{ConfigGenerator: configGenerator}}
			hash, hashWithoutVersion := configGenerator.Hash(), configGenerator.HashWithoutVersion()
			r := &NodePoolReconciler{InstanceTypeProvider: tc.provider, ScaleFromZeroPlatform: tc.providerPlatform}
			err := r.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(configGenerator.Hash()).To(Equal(hash))
			g.Expect(configGenerator.HashWithoutVersion()).To(Equal(hashWithoutVersion))
			if tc.missingScalable {
				return
			}
			actual := object.DeepCopyObject().(client.Object)
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			if tc.expectedError != "" || tc.missingTemplate || tc.platform == hyperv1.OpenStackPlatform {
				g.Expect(actual.GetAnnotations()).To(Equal(object.GetAnnotations()))
				return
			}
			nodeLabels, err := labels.ConvertSelectorToLabelsMap(actual.GetAnnotations()[labelsKey])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(nodeLabels).To(HaveKeyWithValue(corev1.LabelArchStable, "arm64"))
			g.Expect(nodeLabels).To(HaveKeyWithValue("workload", "envoy"))
			if tc.discoverTopology {
				g.Expect(nodeLabels).To(HaveKeyWithValue(corev1.LabelTopologyZone, "eu-central-1b"))
				g.Expect(nodeLabels).To(HaveKeyWithValue(corev1.LabelTopologyRegion, "eu-central-1"))
				g.Expect(nodeLabels).To(HaveKeyWithValue(corev1.LabelFailureDomainBetaZone, "eu-central-1b"))
				g.Expect(nodeLabels).To(HaveKeyWithValue(corev1.LabelFailureDomainBetaRegion, "eu-central-1"))
				g.Expect(nodeLabels).To(HaveKeyWithValue("topology.ebs.csi.aws.com/zone", "eu-central-1b"))
				g.Expect(nodeLabels).To(HaveKeyWithValue("topology.k8s.aws/zone-id", "euc1-az3"))
			} else {
				g.Expect(nodeLabels).ToNot(HaveKey(corev1.LabelTopologyZone))
			}
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(taintsKey, "dedicated=envoy:NoSchedule"))
			g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			if tc.nativeCapacity {
				for _, key := range []string{cpuKey, memoryKey, gpuKey} {
					g.Expect(actual.GetAnnotations()).ToNot(HaveKey(key))
				}
			} else if tc.provider != nil && tc.providerPlatform == tc.platform {
				g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(cpuKey, "4"))
				g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue(memoryKey, "16384"))
				g.Expect(actual.GetAnnotations()).ToNot(HaveKey(gpuKey))
			}
			switch actual := actual.(type) {
			case *capiv1.MachineDeployment:
				g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineDeployment).Spec))
			case *capiv1.MachineSet:
				g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineSet).Spec))
			}
			resourceVersion := actual.GetResourceVersion()
			g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)).To(Succeed())
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			g.Expect(actual.GetResourceVersion()).To(Equal(resourceVersion))
			nodePool.Spec.NodeLabels = map[string]string{"updated": "true"}
			nodePool.Spec.Taints = nil
			g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)).To(Succeed())
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
			nodeLabels, err = labels.ConvertSelectorToLabelsMap(actual.GetAnnotations()[labelsKey])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(nodeLabels).To(HaveKeyWithValue("updated", "true"))
			g.Expect(nodeLabels).ToNot(HaveKey("workload"))
			g.Expect(actual.GetAnnotations()).ToNot(HaveKey(taintsKey))
			if tc.discoverTopology {
				for _, step := range []struct {
					name, zone, zoneID string
					lookupErr          error
				}{
					{name: "When discovered topology changes, it should update every topology alias", zone: "eu-central-1c", zoneID: "euc1-az1"},
					{name: "When discovery fails, it should remove stale topology labels", lookupErr: fmt.Errorf("access denied")},
					{name: "When discovery recovers, it should restore every topology alias", zone: "eu-central-1b", zoneID: "euc1-az3"},
				} {
					t.Run(step.name, func(t *testing.T) {
						g := NewWithT(t)
						ec2Client.zone, ec2Client.zoneID, ec2Client.err = step.zone, step.zoneID, step.lookupErr
						_, err := cpo.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(template)})
						if step.lookupErr != nil {
							g.Expect(err).To(MatchError(ContainSubstring("access denied")))
						} else {
							g.Expect(err).ToNot(HaveOccurred())
						}
						g.Expect(r.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)).To(Succeed())
						g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
						nodeLabels, err := labels.ConvertSelectorToLabelsMap(actual.GetAnnotations()[labelsKey])
						g.Expect(err).ToNot(HaveOccurred())
						for key, value := range map[string]string{
							"failure-domain.beta.kubernetes.io/region": "eu-central-1",
							"failure-domain.beta.kubernetes.io/zone":   step.zone,
							"topology.kubernetes.io/region":            "eu-central-1",
							"topology.kubernetes.io/zone":              step.zone,
							"topology.ebs.csi.aws.com/zone":            step.zone,
							"topology.k8s.aws/zone-id":                 step.zoneID,
						} {
							if step.lookupErr != nil {
								g.Expect(nodeLabels).ToNot(HaveKey(key))
							} else {
								g.Expect(nodeLabels).To(HaveKeyWithValue(key, value))
							}
						}
						g.Expect(nodeLabels).To(HaveKeyWithValue("updated", "true"))
						g.Expect(actual.GetAnnotations()).To(HaveKeyWithValue("custom.io/keep", "preserved"))
						currentTemplate := template.DeepCopyObject().(client.Object)
						g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(template), currentTemplate)).To(Succeed())
						g.Expect(currentTemplate.(*infrav1.AWSMachineTemplate).Spec).To(Equal(template.(*infrav1.AWSMachineTemplate).Spec))
						switch actual := actual.(type) {
						case *capiv1.MachineDeployment:
							g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineDeployment).Spec))
						case *capiv1.MachineSet:
							g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineSet).Spec))
						}
						g.Expect(configGenerator.Hash()).To(Equal(hash))
						g.Expect(configGenerator.HashWithoutVersion()).To(Equal(hashWithoutVersion))
					})
				}
			}
		})
	}
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
		validate        func(g Gomega, md *capiv1.MachineDeployment)
	}{
		{
			name:     "When AWS topology metadata is malformed, it should return an error without changing existing annotations",
			nodePool: &hyperv1.NodePool{},
			object:   &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"custom.io/keep": "preserved"}}},
			machineTemplate: &infrav1.AWSMachineTemplate{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				autoscaling.AWSSubnetTopologyAnnotation: "not-json",
			}}},
			expectErr: true, errSubstring: "failed to decode AWS subnet topology",
		},
		{
			name:     "When AWS topology metadata is incomplete, it should not advertise a guessed zone",
			nodePool: &hyperv1.NodePool{},
			object:   &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"custom.io/keep": "preserved"}}},
			machineTemplate: &infrav1.AWSMachineTemplate{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				autoscaling.AWSSubnetTopologyAnnotation: `{"region":"eu-central-1","zone":"eu-central-1b"}`,
			}}},
			expectErr: true, errSubstring: "missing region, zone, or zone ID",
		},
		{
			name:     "When Azure supplies native capacity and node info, it should keep scheduling metadata without capacity discovery",
			provider: &mockProvider{err: fmt.Errorf("capacity provider must not be queried")},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
				Arch: "amd64", NodeLabels: map[string]string{"workload": "envoy"},
				Taints: []hyperv1.Taint{{Key: "dedicated", Value: "envoy", Effect: corev1.TaintEffectNoSchedule}},
			}},
			object: &capiv1.MachineDeployment{},
			machineTemplate: &capiazure.AzureMachineTemplate{
				Spec: capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{VMSize: "Standard_D4ps_v5"}}},
				Status: capiazure.AzureMachineTemplateStatus{
					Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					NodeInfo: &capiazure.NodeInfo{Architecture: capiazure.ArchitectureArm64},
				},
			},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,workload=envoy"))
				g.Expect(md.Annotations).To(HaveKeyWithValue(taintsKey, "dedicated=envoy:NoSchedule"))
			},
		},
		{
			name:     "When native AWS node info reports ARM architecture, it should prefer the actual architecture without capacity discovery",
			provider: &mockProvider{err: fmt.Errorf("capacity provider must not be queried")},
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{Arch: "amd64"}},
			object:   &capiv1.MachineDeployment{},
			machineTemplate: &infrav1.AWSMachineTemplate{
				Spec: infrav1.AWSMachineTemplateSpec{Template: infrav1.AWSMachineTemplateResource{Spec: infrav1.AWSMachineSpec{InstanceType: "m6g.xlarge"}}},
				Status: infrav1.AWSMachineTemplateStatus{
					Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					NodeInfo: &infrav1.NodeInfo{Architecture: infrav1.ArchitectureArm64},
				},
			},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,node.kubernetes.io/instance-type=m6g.xlarge"))
			},
		},
		{
			name: "When an AWS pool has zero replicas and discovered topology, it should satisfy zone affinity with all known topology labels",
			nodePool: &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
				Arch: "amd64",
				NodeLabels: map[string]string{
					"workload":                                 "envoy",
					"topology.kubernetes.io/zone":              "eu-central-1a",
					"topology.k8s.aws/zone-id":                 "euc1-az1",
					"failure-domain.beta.kubernetes.io/region": "us-east-1",
					"node.kubernetes.io/instance-type":         "m5.large",
				},
			}},
			object: &capiv1.MachineDeployment{Spec: capiv1.MachineDeploymentSpec{Replicas: ptr.To[int32](0)}},
			machineTemplate: &infrav1.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					autoscaling.AWSSubnetTopologyAnnotation: `{"region":"eu-central-1","zone":"eu-central-1b","zoneID":"euc1-az3"}`,
				}},
				Spec: infrav1.AWSMachineTemplateSpec{Template: infrav1.AWSMachineTemplateResource{
					Spec: infrav1.AWSMachineSpec{InstanceType: "c5.24xlarge"},
				}},
				Status: infrav1.AWSMachineTemplateStatus{Capacity: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("96"),
					corev1.ResourceMemory: resource.MustParse("192Gi"),
				}},
			},
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.Annotations[labelsKey]).To(Equal("failure-domain.beta.kubernetes.io/region=eu-central-1,failure-domain.beta.kubernetes.io/zone=eu-central-1b,kubernetes.io/arch=amd64,node.kubernetes.io/instance-type=c5.24xlarge,topology.ebs.csi.aws.com/zone=eu-central-1b,topology.k8s.aws/zone-id=euc1-az3,topology.kubernetes.io/region=eu-central-1,topology.kubernetes.io/zone=eu-central-1b,workload=envoy"))
				nodeLabels, err := labels.ConvertSelectorToLabelsMap(md.Annotations[labelsKey])
				g.Expect(err).ToNot(HaveOccurred())
				requiredZone, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{{
						Key: corev1.LabelTopologyZone, Operator: metav1.LabelSelectorOpIn, Values: []string{"eu-central-1b"},
					}},
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(requiredZone.Matches(nodeLabels)).To(BeTrue())
				g.Expect(ptr.Deref(md.Spec.Replicas, -1)).To(BeZero())
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
			provider:        &mockProvider{err: fmt.Errorf("failed to describe instance type")},
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       true,
			errSubstring:    "failed to describe instance type",
		},
		{
			name:     "When native capacity is available, it should reconcile labels and taints without querying the capacity provider",
			provider: &mockProvider{err: fmt.Errorf("capacity provider must not be queried")},
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch:       "amd64",
					NodeLabels: map[string]string{"workload": "envoy"},
					Taints: []hyperv1.Taint{
						{Key: "dedicated", Value: "envoy", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
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
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64,node.kubernetes.io/instance-type=m5.large,workload=envoy"))
				g.Expect(a).To(HaveKeyWithValue(taintsKey, "dedicated=envoy:NoSchedule"))
				g.Expect(a).To(HaveKeyWithValue("custom.io/keep", "preserved"))
			},
		},
		{
			name:            "When provider is nil, it should reconcile scheduling metadata without capacity annotations",
			provider:        nil,
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAWSTemplate("m5.large"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.GetAnnotations()).ToNot(HaveKey(cpuKey))
				g.Expect(md.GetAnnotations()).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64,node.kubernetes.io/instance-type=m5.large"))
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
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=amd64,node.kubernetes.io/instance-type=m5.large"))
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
			name:            "When Azure has no legacy provider, it should reconcile scheduling metadata without capacity annotations",
			provider:        nil,
			nodePool:        &hyperv1.NodePool{},
			object:          &capiv1.MachineDeployment{},
			machineTemplate: newAzureTemplate("Standard_D4s_v5"),
			expectErr:       false,
			validate: func(g Gomega, md *capiv1.MachineDeployment) {
				g.Expect(md.GetAnnotations()).ToNot(HaveKey(cpuKey))
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
				g.Expect(a).To(HaveKeyWithValue(labelsKey, "env=production,kubernetes.io/arch=arm64,node.kubernetes.io/instance-type=p3.2xlarge"))
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
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstring))
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
