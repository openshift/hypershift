package nodepool

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestHasStatusCapacity(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		template client.Object
		expect   bool
	}{
		{
			name: "When AWSMachineTemplate has Status.Capacity, it should return true",
			template: &capiaws.AWSMachineTemplate{
				Status: capiaws.AWSMachineTemplateStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
			},
			expect: true,
		},
		{
			name:     "When AWSMachineTemplate has empty Status.Capacity, it should return false",
			template: &capiaws.AWSMachineTemplate{},
			expect:   false,
		},
		{
			name: "When AzureMachineTemplate has Status.Capacity, it should return true",
			template: &capiazure.AzureMachineTemplate{
				Status: capiazure.AzureMachineTemplateStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
			},
			expect: true,
		},
		{
			name:     "When AzureMachineTemplate has empty Status.Capacity, it should return false",
			template: &capiazure.AzureMachineTemplate{},
			expect:   false,
		},
		{
			name:     "When template is an unsupported type, it should return false",
			template: &capikubevirt.KubevirtMachineTemplate{},
			expect:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hasStatusCapacity(tc.template)).To(Equal(tc.expect))
		})
	}
}

func TestSetMachineSetReplicas(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineSet                  *capiv1.MachineSet
		scaleFromZeroSupported      bool
		expectReplicas              int32
		expectAutoscalerAnnotations map[string]string
	}{
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineSet has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it does not set current replicas but set annotations when autoscaling is enabled" +
				" and the MachineSet has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.min and set annotations when autoscaling is enabled" +
				" and the MachineSet has replicas < autoScaling.min",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: ptr.To[int32](1),
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.max and set annotations when autoscaling is enabled" +
				" and the MachineSet has replicas > autoScaling.max",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: ptr.To[int32](10),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "When scale-from-zero is supported, it should allow min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: nil,
				},
			},
			scaleFromZeroSupported: true,
			expectReplicas:         0,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "0",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "When scale-from-zero is not supported, it should enforce min=1 even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it enforces min=1 for KubeVirt platform even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it enforces min=1 for Agent platform even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineSetSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineSetReplicas(tc.nodePool, tc.machineSet, tc.scaleFromZeroSupported)
			g.Expect(*tc.machineSet.Spec.Replicas).To(Equal(tc.expectReplicas))
			g.Expect(tc.machineSet.Annotations).To(Equal(tc.expectAutoscalerAnnotations))
		})
	}
}

func TestSetMachineDeploymentReplicas(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineDeployment           *capiv1.MachineDeployment
		scaleFromZeroSupported      bool
		expectReplicas              int32
		expectAutoscalerAnnotations map[string]string
	}{
		{
			name: "it sets replicas when autoscaling is disabled",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Replicas: ptr.To[int32](5),
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "0",
				autoscalerMaxAnnotation: "0",
			},
		},
		{
			name: "it keeps current replicas and set annotations when autoscaling is enabled",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
			},
			expectReplicas: 3,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has not been created yet",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{},
			expectReplicas:    1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has 0 replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to 1 and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](1),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it does not set current replicas but set annotations when autoscaling is enabled" +
				" and the MachineDeployment has nil replicas",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: nil,
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.min and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has replicas < autoScaling.min",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](1),
				},
			},
			expectReplicas: 2,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it sets current replicas to autoScaling.max and set annotations when autoscaling is enabled" +
				" and the MachineDeployment has replicas > autoScaling.max",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](2),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](10),
				},
			},
			expectReplicas: 5,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "2",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "When scale-from-zero is supported, it should allow min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: nil,
				},
			},
			scaleFromZeroSupported: true,
			expectReplicas:         0,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "0",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "When scale-from-zero is not supported, it should enforce min=1 even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it enforces min=1 for KubeVirt platform even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
		{
			name: "it enforces min=1 for Agent platform even when NodePool specifies min=0",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AgentPlatform,
					},
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](0),
						Max: 5,
					},
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Now(),
				},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](0),
				},
			},
			expectReplicas: 1,
			expectAutoscalerAnnotations: map[string]string{
				autoscalerMinAnnotation: "1",
				autoscalerMaxAnnotation: "5",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineDeploymentReplicas(tc.nodePool, tc.machineDeployment, tc.scaleFromZeroSupported)
			g.Expect(*tc.machineDeployment.Spec.Replicas).To(Equal(tc.expectReplicas))
			g.Expect(tc.machineDeployment.Annotations).To(Equal(tc.expectAutoscalerAnnotations))
		})
	}
}

// It returns a expected machineTemplateSpecJSON
// and a template and mutateTemplate able to produce an expected target template.
func RunTestMachineTemplateBuilders(t *testing.T, preCreateMachineTemplate bool) {
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client: c,
	}

	infraID := "test"
	ami := "test"
	hcluster := &hyperv1.HostedCluster{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{},
			InfraID: infraID,
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					ResourceTags: nil,
				},
			},
		},
		Status: hyperv1.HostedClusterStatus{
			Platform: &hyperv1.PlatformStatus{
				AWS: &hyperv1.AWSPlatformStatus{
					DefaultWorkerSecurityGroupID: "default-sg",
				},
			},
		},
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					InstanceType:    "",
					InstanceProfile: "",
					Subnet:          hyperv1.AWSResourceReference{ID: ptr.To("subnet-xyz")},
					AMI:             ami,
					RootVolume: &hyperv1.Volume{
						Size: 16,
						Type: "io1",
						IOPS: 5000,
					},
					ResourceTags: nil,
				},
			},
		},
	}

	if preCreateMachineTemplate {
		preCreatedMachineTemplate := &capiaws.AWSMachineTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodePool.GetName(),
				Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
			},
			Spec: capiaws.AWSMachineTemplateSpec{
				Template: capiaws.AWSMachineTemplateResource{
					Spec: capiaws.AWSMachineSpec{
						AMI: capiaws.AMIReference{
							ID: ptr.To(ami),
						},
						IAMInstanceProfile:   "test-worker-profile",
						Subnet:               &capiaws.AWSResourceReference{},
						UncompressedUserData: ptr.To(true),
					},
				},
			},
		}
		err := r.Create(t.Context(), preCreatedMachineTemplate)
		g.Expect(err).ToNot(HaveOccurred())
	}

	expectedMachineTemplate := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nodePool.GetName(),
			Namespace:   manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					AMI: capiaws.AMIReference{
						ID: ptr.To(ami),
					},
					IAMInstanceProfile: "test-worker-profile",
					Subnet: &capiaws.AWSResourceReference{
						ID: ptr.To("subnet-xyz"),
					},
					UncompressedUserData: ptr.To(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					AdditionalTags: capiaws.Tags{
						awsClusterCloudProviderTagKey(infraID): infraLifecycleOwned,
					},
					AdditionalSecurityGroups: []capiaws.AWSResourceReference{
						{
							ID: ptr.To("default-sg"),
						},
					},
					RootVolume: &capiaws.Volume{
						Size: 16,
						Type: "io1",
						IOPS: 5000,
					},
					InstanceMetadataOptions: &capiaws.InstanceMetadataOptions{
						HTTPTokens:              capiaws.HTTPTokensStateOptional,
						HTTPPutResponseHopLimit: 2,
						HTTPEndpoint:            capiaws.InstanceMetadataEndpointStateEnabled,
						InstanceMetadataTags:    capiaws.InstanceMetadataEndpointStateDisabled,
					},
				},
			},
		},
	}
	expectedMachineTemplateSpecJSON, err := json.Marshal(expectedMachineTemplate.Spec)
	g.Expect(err).ToNot(HaveOccurred())

	expectedMachineTemplate.SetName(generateMachineTemplateName(nodePool, expectedMachineTemplateSpecJSON))

	capi := &CAPI{
		Token: &Token{
			ConfigGenerator: &ConfigGenerator{
				hostedCluster: hcluster,
				nodePool:      nodePool,
				Client:        c,
				rolloutConfig: &rolloutConfig{
					releaseImage: &releaseinfo.ReleaseImage{},
				},
			},
			cpoCapabilities: &CPOCapabilities{
				CreateDefaultAWSSecurityGroup: true,
			},
		},
		capiClusterName: "test",
	}
	template, err := capi.machineTemplateBuilders(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	machineTemplateSpec := template.(*capiaws.AWSMachineTemplate).Spec
	g.Expect(machineTemplateSpec).To(BeEquivalentTo(expectedMachineTemplate.Spec))

	// Validate that template and mutateTemplate are able to produce an expected target template.
	_, err = upsert.NewApplyProvider(false).ApplyManifest(t.Context(), r.Client, template)
	g.Expect(err).ToNot(HaveOccurred())

	gotMachineTemplate := &capiaws.AWSMachineTemplate{}
	g.Expect(r.Client.Get(t.Context(), client.ObjectKeyFromObject(template), gotMachineTemplate)).To(Succeed())
	g.Expect(expectedMachineTemplate.Spec).To(BeEquivalentTo(gotMachineTemplate.Spec))
	g.Expect(expectedMachineTemplate.ObjectMeta.Annotations).To(BeEquivalentTo(gotMachineTemplate.ObjectMeta.Annotations))
}

func TestMachineTemplateBuilders(t *testing.T) {
	t.Parallel()
	RunTestMachineTemplateBuilders(t, false)
}

func TestMachineTemplateBuildersPreexisting(t *testing.T) {
	t.Parallel()
	RunTestMachineTemplateBuilders(t, true)
}

func TestCleanupMachineTemplates(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	template1 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template1",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}

	template2 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template2",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}

	gvk, err := apiutil.GVKForObject(template1, api.Scheme)
	g.Expect(err).ToNot(HaveOccurred())
	// machine set referencing template1
	ms := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "machineSet",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiv1.MachineSetSpec{
			Template: capiv1.MachineTemplateSpec{
				Spec: capiv1.MachineSpec{
					InfrastructureRef: capiv1.ContractVersionedObjectReference{
						Kind:     gvk.Kind,
						APIGroup: gvk.Group,
						Name:     template1.Name,
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool, template1, template2, ms).Build()
	capi := &CAPI{
		Token: &Token{
			ConfigGenerator: &ConfigGenerator{
				Client:   c,
				nodePool: nodePool,
			},
		},
	}

	err = capi.cleanupMachineTemplates(t.Context(), logr.Discard(), nodePool, "test")
	g.Expect(err).ToNot(HaveOccurred())

	templates, err := capi.listMachineTemplates()
	g.Expect(err).ToNot(HaveOccurred())
	// check template2 has been deleted
	g.Expect(len(templates)).To(Equal(1))
	g.Expect(templates[0].GetName()).To(Equal("template1"))
}

func TestListMachineTemplatesAWS(t *testing.T) {
	g := NewWithT(t)
	_ = capiaws.AddToScheme(api.Scheme)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
	g.Expect(r.Client.Create(t.Context(), nodePool)).To(BeNil())

	// MachineTemplate with the expected annotation
	template1 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template1",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(t.Context(), template1)).To(BeNil())

	// MachineTemplate without the expected annotation
	template2 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "template2",
			Namespace: "test",
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(t.Context(), template2)).To(BeNil())

	capi := &CAPI{
		Token: &Token{
			ConfigGenerator: &ConfigGenerator{
				Client:   c,
				nodePool: nodePool,
			},
		},
	}
	templates, err := capi.listMachineTemplates()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(templates)).To(Equal(1))
	g.Expect(templates[0].GetName()).To(Equal("template1"))
}

func TestListMachineTemplatesIBMCloud(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()
	r := &NodePoolReconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.IBMCloudPlatform,
			},
		},
	}
	g.Expect(r.Client.Create(t.Context(), nodePool)).To(BeNil())

	capi := &CAPI{
		Token: &Token{
			ConfigGenerator: &ConfigGenerator{
				Client:   c,
				nodePool: nodePool,
			},
		},
	}
	templates, err := capi.listMachineTemplates()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(len(templates)).To(Equal(0))
}

func TestInPlaceUpgradeMaxUnavailable(t *testing.T) {
	t.Parallel()
	intPointer1 := intstr.FromInt(1)
	intPointer2 := intstr.FromInt(2)
	strPointer10 := intstr.FromString("10%")
	strPointer75 := intstr.FromString("75%")
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expect   int
	}{
		{
			name: "defaults to 1 when no maxUnavailable specified",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{},
					},
					Replicas: ptr.To[int32](4),
				},
			},
			expect: 1,
		},
		{
			name: "can handle default value of 1",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &intPointer1,
						},
					},
					Replicas: ptr.To[int32](4),
				},
			},
			expect: 1,
		},
		{
			name: "can handle other values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &intPointer2,
						},
					},
					Replicas: ptr.To[int32](4),
				},
			},
			expect: 2,
		},
		{
			name: "can handle percent values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &strPointer75,
						},
					},
					Replicas: ptr.To[int32](4),
				},
			},
			expect: 3,
		},
		{
			name: "can handle roundable values",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{
						InPlace: &hyperv1.InPlaceUpgrade{
							MaxUnavailable: &strPointer10,
						},
					},
					Replicas: ptr.To[int32](4),
				},
			},
			expect: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			maxUnavailable, err := getInPlaceMaxUnavailable(tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(maxUnavailable).To(Equal(tc.expect))
		})
	}
}

func TestTaintsToJSON(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		taints   []hyperv1.Taint
		expected string
	}{
		{
			name:     "",
			taints:   []hyperv1.Taint{},
			expected: "[]",
		},
		{
			name: "",
			taints: []hyperv1.Taint{
				{
					Key:    "foo",
					Value:  "bar",
					Effect: "any",
				},
				{
					Key:    "foo2",
					Value:  "bar2",
					Effect: "any",
				},
			},
			expected: `[{"key":"foo","value":"bar","effect":"any"},{"key":"foo2","value":"bar2","effect":"any"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			taints, err := taintsToJSON(tc.taints)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(taints).To(BeEquivalentTo(tc.expected))

			// validate decoding.
			var coreTaints []corev1.Taint
			err = json.Unmarshal([]byte(taints), &coreTaints)
			g.Expect(err).ToNot(HaveOccurred())
			node := &corev1.Node{}
			node.Spec.Taints = append(node.Spec.Taints, coreTaints...)
			g.Expect(node.Spec.Taints).To(ContainElements(coreTaints))
		})
	}
}

func TestReconcileMachineHealthCheck(t *testing.T) {
	t.Parallel()
	hostedcluster := func(opts ...func(client.Object)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster"}}
		for _, o := range opts {
			o(hc)
		}
		return hc
	}

	nodepool := func(opts ...func(client.Object)) *hyperv1.NodePool {
		np := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nodepool"}}
		np.Spec.ClusterName = "cluster"
		for _, o := range opts {
			o(np)
		}
		return np
	}

	defaultMaxUnhealthy := intstr.Parse("2")
	healthcheck := func(opts ...func(*capiv1.MachineHealthCheck)) *capiv1.MachineHealthCheck {
		mhc := &capiv1.MachineHealthCheck{ObjectMeta: metav1.ObjectMeta{Namespace: "ns-cluster", Name: "nodepool"}}
		resName := generateName("cluster", "cluster", "nodepool")
		timeoutSeconds := int32(480)
		nodeStartupTimeoutSeconds := int32(1200)
		mhc.Spec = capiv1.MachineHealthCheckSpec{
			ClusterName: "cluster",
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					resName: resName,
				},
			},
			Checks: capiv1.MachineHealthCheckChecks{
				UnhealthyNodeConditions: []capiv1.UnhealthyNodeCondition{
					{
						Type:           corev1.NodeReady,
						Status:         corev1.ConditionFalse,
						TimeoutSeconds: &timeoutSeconds,
					},
					{
						Type:           corev1.NodeReady,
						Status:         corev1.ConditionUnknown,
						TimeoutSeconds: &timeoutSeconds,
					},
				},
				NodeStartupTimeoutSeconds: &nodeStartupTimeoutSeconds,
			},
			Remediation: capiv1.MachineHealthCheckRemediation{
				TriggerIf: capiv1.MachineHealthCheckRemediationTriggerIf{
					UnhealthyLessThanOrEqualTo: &defaultMaxUnhealthy,
				},
			},
		}
		for _, o := range opts {
			o(mhc)
		}
		return mhc
	}

	withTimeoutOverride := func(value string) func(client.Object) {
		return func(o client.Object) {
			a := o.GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a[hyperv1.MachineHealthCheckTimeoutAnnotation] = value
			o.SetAnnotations(a)
		}
	}

	withMaxUnhealthyOverride := func(value string) func(client.Object) {
		return func(o client.Object) {
			a := o.GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation] = value
			o.SetAnnotations(a)
		}
	}
	withTimeout := func(d time.Duration) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			s := int32(d.Seconds())
			for i := range mhc.Spec.Checks.UnhealthyNodeConditions {
				mhc.Spec.Checks.UnhealthyNodeConditions[i].TimeoutSeconds = &s
			}
		}
	}
	withNodeStartupTimeoutOverride := func(value string) func(client.Object) {
		return func(o client.Object) {
			a := o.GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a[hyperv1.MachineHealthCheckNodeStartupTimeoutAnnotation] = value
			o.SetAnnotations(a)
		}
	}
	withNodeStartupTimeout := func(d time.Duration) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			s := int32(d.Seconds())
			mhc.Spec.Checks.NodeStartupTimeoutSeconds = &s
		}
	}

	tests := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		np       *hyperv1.NodePool
		expected *capiv1.MachineHealthCheck
	}{
		{
			name:     "defaults",
			hc:       hostedcluster(),
			np:       nodepool(),
			expected: healthcheck(),
		},
		{
			name:     "timeout override in hc",
			hc:       hostedcluster(withTimeoutOverride("10m")),
			np:       nodepool(),
			expected: healthcheck(withTimeout(10 * time.Minute)),
		},
		{
			name:     "timeout override in np",
			hc:       hostedcluster(),
			np:       nodepool(withTimeoutOverride("40m")),
			expected: healthcheck(withTimeout(40 * time.Minute)),
		},
		{
			name:     "timeout override in both, np takes precedence",
			hc:       hostedcluster(withTimeoutOverride("10m")),
			np:       nodepool(withTimeoutOverride("40m")),
			expected: healthcheck(withTimeout(40 * time.Minute)),
		},
		{
			name:     "invalid timeout override, retains default",
			hc:       hostedcluster(withTimeoutOverride("foo")),
			np:       nodepool(),
			expected: healthcheck(),
		},
		{
			name:     "node startup timeout override in hc",
			hc:       hostedcluster(withNodeStartupTimeoutOverride("10m")),
			np:       nodepool(),
			expected: healthcheck(withNodeStartupTimeout(10 * time.Minute)),
		},
		{
			name:     "node startup timeout override in np",
			hc:       hostedcluster(),
			np:       nodepool(withNodeStartupTimeoutOverride("40m")),
			expected: healthcheck(withNodeStartupTimeout(40 * time.Minute)),
		},
		{
			name:     "node startup timeout override in both, np takes precedence",
			hc:       hostedcluster(withNodeStartupTimeoutOverride("10m")),
			np:       nodepool(withNodeStartupTimeoutOverride("40m")),
			expected: healthcheck(withNodeStartupTimeout(40 * time.Minute)),
		},
		{
			name:     "node startup invalid timeout override, retains default",
			hc:       hostedcluster(withNodeStartupTimeoutOverride("foo")),
			np:       nodepool(),
			expected: healthcheck(),
		},
		{
			name:     "invalid maxunhealthy override value, default is preserved",
			hc:       hostedcluster(),
			np:       nodepool(withMaxUnhealthyOverride("foo")),
			expected: healthcheck(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						hostedCluster: tt.hc,
						nodePool:      tt.np,
					},
				},
				capiClusterName: "cluster",
			}
			mhc := &capiv1.MachineHealthCheck{}
			err := capi.reconcileMachineHealthCheck(t.Context(), mhc)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mhc.Spec).To(testutil.MatchExpected(tt.expected.Spec))
		})
	}
}

func TestCAPIReconcile(t *testing.T) {
	t.Parallel()
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)
	// This is the generated name by machineTemplateBuilders.
	// So reconciliation doesn't create a new AWSMachineTemplate but reconcile this one.
	awsMachineTemplateName := "test-nodepool-28d5cf5a"
	capiClusterName := "infra-id"

	tests := []struct {
		name          string
		nodePool      *hyperv1.NodePool
		hostedCluster *hyperv1.HostedCluster
		machineSet    *capiv1.MachineSet
		templates     []client.Object
		// Different userdata name is what triggers a machineDeployment rollout.
		// Set this to true to validate expectations that should happen when no rollout is happening.
		sameUserData  bool
		expectedError bool
	}{
		{
			name: "When a new userdata secret is generated, it should reconcile successfully",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
						AutoRepair: false,
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							// Set an ami to avoid stub the defaultAMI func.
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
					Labels: map[string]string{
						capiv1.MachineDeploymentNameLabel: "test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								// This is the generated name by machineTemplateBuilders.
								// So reconciliation doesn't create a new AWSMachineTemplate but reconcile this one.
								Name: awsMachineTemplateName,
							},
						},
					},
				},
			},
			templates: []client.Object{
				// Do not match MachineSet infraRef. It will be cleaned up.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "does-not-match-infra-ref-name",
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				// Match NodePool and infraRef, reconciliation should keep it.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name:         "When NO userdata secret is generated, it should include rollout complete annotations reconcile successfully",
			sameUserData: true,
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
						AutoRepair: false,
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							// Set an ami to avoid stub the defaultAMI func.
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
					Labels: map[string]string{
						capiv1.MachineDeploymentNameLabel: "test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								// This is the generated name by machineTemplateBuilders.
								// So reconciliation doesn't create a new AWSMachineTemplate but reconcile this one.
								Name: awsMachineTemplateName,
							},
						},
					},
				},
			},
			templates: []client.Object{
				// Do not match MachineSet infraRef. It will be cleaned up.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "does-not-match-infra-ref-name",
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				// Match NodePool and infraRef, reconciliation should keep it.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "When auto repair and autoscaling are enabled, it should reconcile MHCs and autoscaler labels",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
						AutoRepair: true,
					},
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](3),
						Max: 10,
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							// Set an ami to avoid stub the defaultAMI func.
							AMI: "an-ami",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{
					Conditions: []hyperv1.NodePoolCondition{
						{
							Type:   hyperv1.NodePoolReachedIgnitionEndpoint,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
					Labels: map[string]string{
						capiv1.MachineDeploymentNameLabel: "test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								// This is the generated name by machineTemplateBuilders.
								// So reconciliation doesn't create a new AWSMachineTemplate but reconcile this one.
								Name: awsMachineTemplateName,
							},
						},
					},
				},
			},
			templates: []client.Object{
				// Do not match MachineSet infraRef. It will be cleaned up.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "does-not-match-infra-ref-name",
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				// Match NodePool and infraRef, reconciliation should keep it.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedError: false,
		},
		// {
		// 	name: "error during machine template cleanup",
		// 	nodePool: &hyperv1.NodePool{
		// 		ObjectMeta: metav1.ObjectMeta{
		// 			Name:      "test-nodepool",
		// 			Namespace: "test-namespace",
		// 		},
		// 	},
		// 	hostedCluster: &hyperv1.HostedCluster{
		// 		ObjectMeta: metav1.ObjectMeta{
		// 			Name:      "test-cluster",
		// 			Namespace: "test-namespace",
		// 		},
		// 	},
		// 	expectedError: true,
		// 	templates:     []client.Object{},
		// },
		{
			name: "When spot is enabled, it should create spot MHC and add interruptible label",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						AnnotationEnableSpot: "true",
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
						AutoRepair: false,
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								Name:     awsMachineTemplateName,
							},
						},
					},
				},
			},
			templates: []client.Object{
				// Do not match MachineSet infraRef. It will be cleaned up.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "does-not-match-infra-ref-name",
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				// Match NodePool and infraRef, reconciliation should keep it.
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "When NodeDrainTimeout and NodeVolumeDetachTimeout are set, they should propagate to MachineDeployment",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
						AutoRepair: false,
					},
					Replicas:                ptr.To[int32](3),
					NodeDrainTimeout:        &metav1.Duration{Duration: 10 * time.Minute},
					NodeVolumeDetachTimeout: &metav1.Duration{Duration: 5 * time.Minute},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			machineSet: &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								Name:     awsMachineTemplateName,
							},
						},
					},
				},
			},
			templates: []client.Object{
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "does-not-match-infra-ref-name",
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				// WithObjectTracker()
				// WithInterceptorFuncs()
				WithObjects(tt.nodePool, tt.machineSet).
				WithObjects(tt.templates...).
				Build()

			controlpaneNamespace := manifests.HostedControlPlaneNamespace(tt.hostedCluster.Namespace, tt.hostedCluster.Name)
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                c,
						hostedCluster:         tt.hostedCluster,
						nodePool:              tt.nodePool,
						controlplaneNamespace: controlpaneNamespace,
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: "target-version",
									},
								},
							},
						},
					},
					cpoCapabilities:        &CPOCapabilities{},
					CreateOrUpdateProvider: upsert.New(false),
				},
				capiClusterName: capiClusterName,
				ApplyProvider:   upsert.NewApplyProvider(false),
			}

			// Make sure the templates are populates in the control plane namespace
			templateList := &capiaws.AWSMachineTemplateList{}
			err := capi.Client.List(t.Context(), templateList, client.InNamespace(controlpaneNamespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(templateList.Items).To(HaveLen(2))

			err = capi.Reconcile(t.Context())
			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())

				// Check that old machine templates are deleted.
				templateList := &capiaws.AWSMachineTemplateList{}
				err := capi.Client.List(t.Context(), templateList, client.InNamespace(controlpaneNamespace))
				g.Expect(err).NotTo(HaveOccurred())
				// Expect templates which does not match the ref to be deleted.
				g.Expect(templateList.Items).To(HaveLen(1))
				g.Expect(templateList.Items[0].GetName()).To(Equal(awsMachineTemplateName))

				// Check MachineDeployment.
				md := &capiv1.MachineDeployment{}
				err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: "test-nodepool"}, md)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(md.Spec.Replicas).To(Equal(ptr.To[int32](3)))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(Equal(awsMachineTemplateName))
				// Check MachineDeployment annotations
				g.Expect(md.Annotations).To(HaveKeyWithValue(nodePoolAnnotation, "test-namespace/test-nodepool"))

				// Check MachineDeployment spec.
				g.Expect(md.Spec.Rollout.Strategy.Type).To(Equal(capiv1.MachineDeploymentRolloutStrategyType("RollingUpdate")))
				g.Expect(md.Spec.Rollout.Strategy.RollingUpdate.MaxUnavailable.IntValue()).To(Equal(0))
				g.Expect(md.Spec.Rollout.Strategy.RollingUpdate.MaxSurge.IntValue()).To(Equal(1))

				// Check MachineDeployment labels.
				g.Expect(md.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, capiClusterName))

				// Check MachineDeployment template labels.
				g.Expect(md.Spec.Template.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, capiClusterName))

				// Check MachineDeployment annotations labels.
				g.Expect(md.Spec.Template.Annotations).To(HaveKeyWithValue(nodePoolAnnotation, "test-namespace/test-nodepool"))

				// Check MachineDeployment template spec
				g.Expect(md.Spec.Template.Spec.ClusterName).To(Equal(capiClusterName))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.APIGroup).To(Equal("infrastructure.cluster.x-k8s.io"))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Kind).To(Equal("AWSMachineTemplate"))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(Equal(awsMachineTemplateName))

				g.Expect(md.Spec.Template.Spec.Version).To(Equal("target-version"))
				g.Expect(md.Spec.Template.Spec.Deletion.NodeDrainTimeoutSeconds).To(Equal(durationToSeconds(tt.nodePool.Spec.NodeDrainTimeout)))
				g.Expect(md.Spec.Template.Spec.Deletion.NodeVolumeDetachTimeoutSeconds).To(Equal(durationToSeconds(tt.nodePool.Spec.NodeVolumeDetachTimeout)))

				// Check Bootstrap DataSecretName.
				g.Expect(md.Spec.Template.Spec.Bootstrap.DataSecretName).NotTo(BeNil())
				g.Expect(*md.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal("user-data-test-nodepool-ac51f7c1"))
				g.Expect(*md.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal(capi.UserDataSecret().GetName()))

				// Check autoscaling annotations.
				if tt.nodePool.Spec.AutoScaling != nil {
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, fmt.Sprintf("%d", tt.nodePool.Spec.AutoScaling.Max)))
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, fmt.Sprintf("%d", *tt.nodePool.Spec.AutoScaling.Min)))
				} else {
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, "0"))
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, "0"))
				}

				// Check MachineHealthCheck
				if tt.nodePool.Spec.Management.AutoRepair {
					mhc := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, mhc)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(mhc.Spec.ClusterName).To(Equal(capiClusterName))
				} else {
					mhc := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: "test-cp-namespace", Name: tt.nodePool.GetName()}, mhc)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}

				// Check spot MHC and interruptible label
				if isSpotEnabled(tt.nodePool) {
					// Spot MHC should exist
					spotMHC := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName() + "-spot"}, spotMHC)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(spotMHC.Spec.Selector.MatchLabels).To(HaveKeyWithValue(interruptibleInstanceLabel, ""))

					// MachineDeployment template should have interruptible label
					g.Expect(md.Spec.Template.Labels).To(HaveKeyWithValue(interruptibleInstanceLabel, ""))
				} else {
					// Spot MHC should not exist
					spotMHC := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName() + "-spot"}, spotMHC)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

					// MachineDeployment template should not have interruptible label
					g.Expect(md.Spec.Template.Labels).ToNot(HaveKey(interruptibleInstanceLabel))
				}

				if tt.sameUserData {
					// Get the MachineDeployment.
					md := &capiv1.MachineDeployment{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, md)
					g.Expect(err).NotTo(HaveOccurred())

					// Update MachineDeployment status to indicate rollout is complete.
					md.Status.Replicas = tt.nodePool.Spec.Replicas
					md.Status.ReadyReplicas = tt.nodePool.Spec.Replicas
					md.Status.AvailableReplicas = tt.nodePool.Spec.Replicas
					md.Status.UpToDateReplicas = tt.nodePool.Spec.Replicas
					md.Status.ObservedGeneration = md.Generation
					err = capi.Client.Update(t.Context(), md)
					g.Expect(err).NotTo(HaveOccurred())

					md = &capiv1.MachineDeployment{}
					err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, md)
					g.Expect(err).NotTo(HaveOccurred())

					// Re-run reconcile.
					err = capi.Reconcile(t.Context())
					g.Expect(err).NotTo(HaveOccurred())

					// Check for the expected annotations.
					// TODO(alberto): reconcileMachineDeployment mutate the NodePool with this annotations and status.version.
					// We should decouple that logic from that func into the core nodepool controller.
					// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
					g.Expect(tt.nodePool.Annotations).To(HaveKeyWithValue(nodePoolAnnotationPlatformMachineTemplate, awsMachineTemplateName))
					g.Expect(tt.nodePool.Annotations).To(HaveKeyWithValue(nodePoolAnnotationCurrentConfig, Not(BeEmpty())))
					g.Expect(tt.nodePool.Annotations).To(HaveKeyWithValue(nodePoolAnnotationCurrentConfig, capi.HashWithoutVersion()))
					g.Expect(tt.nodePool.Status.Version).To(Equal(capi.Version()))
				} else {
					g.Expect(tt.nodePool.Annotations).ToNot(HaveKey(nodePoolAnnotationPlatformMachineTemplate))
					g.Expect(tt.nodePool.Annotations).ToNot(HaveKey(nodePoolAnnotationCurrentConfig))
					g.Expect(tt.nodePool.Status.Version).To(BeEmpty())
				}
			}
		})
	}
}

// TestCAPIReconcile_machineset is specific for UpgradeTypeInPlace.
func TestCAPIReconcile_machineset(t *testing.T) {
	t.Parallel()
	awsMachineTemplateName := "test-nodepool-28d5cf5a"
	capiClusterName := "infra-id"

	tests := []struct {
		name     string
		nodePool *hyperv1.NodePool
	}{
		{
			name: "When NodeDrainTimeout and NodeVolumeDetachTimeout are set, they should propagate to MachineSet",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeInPlace,
						InPlace:     &hyperv1.InPlaceUpgrade{},
					},
					Replicas:                ptr.To[int32](3),
					NodeDrainTimeout:        &metav1.Duration{Duration: 10 * time.Minute},
					NodeVolumeDetachTimeout: &metav1.Duration{Duration: 5 * time.Minute},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
		},
		{
			name: "When NodeDrainTimeout and NodeVolumeDetachTimeout are nil, they should propagate as nil to MachineSet",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeInPlace,
						InPlace:     &hyperv1.InPlaceUpgrade{},
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			}

			existingMachineSet := &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.nodePool.GetName(),
					Namespace: "test-namespace-test-cluster",
					Annotations: map[string]string{
						nodePoolAnnotation: "test-namespace/test-nodepool",
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Kind:     "AWSMachineTemplate",
								APIGroup: "infrastructure.cluster.x-k8s.io",
								Name:     awsMachineTemplateName,
							},
						},
					},
				},
			}

			templates := []client.Object{
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: "test-namespace-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tt.nodePool, existingMachineSet).
				WithObjects(templates...).
				Build()

			controlplaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                c,
						hostedCluster:         hostedCluster,
						nodePool:              tt.nodePool,
						controlplaneNamespace: controlplaneNamespace,
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: "target-version",
									},
								},
							},
						},
					},
					cpoCapabilities:        &CPOCapabilities{},
					CreateOrUpdateProvider: upsert.New(false),
				},
				capiClusterName: capiClusterName,
				ApplyProvider:   upsert.NewApplyProvider(false),
			}

			err := capi.Reconcile(t.Context())
			g.Expect(err).NotTo(HaveOccurred())

			ms := &capiv1.MachineSet{}
			err = capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlplaneNamespace, Name: tt.nodePool.GetName()}, ms)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ms.Spec.Template.Spec.Deletion.NodeDrainTimeoutSeconds).To(Equal(durationToSeconds(tt.nodePool.Spec.NodeDrainTimeout)))
			g.Expect(ms.Spec.Template.Spec.Deletion.NodeVolumeDetachTimeoutSeconds).To(Equal(durationToSeconds(tt.nodePool.Spec.NodeVolumeDetachTimeout)))
		})
	}
}

func TestGlobalPSManagedLabelOnMachines(t *testing.T) {
	t.Parallel()
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)
	awsMachineTemplateName := "test-nodepool-29e4de4b"
	capiClusterName := "infra-id"
	controlPlaneNamespace := "test-namespace-test-cluster"

	tests := []struct {
		name          string
		nodePool      *hyperv1.NodePool
		hostedCluster *hyperv1.HostedCluster
		objects       []client.Object
		reconcile     func(t *testing.T, capi *CAPI) error
		expectLabel   bool
	}{
		{
			name: "When using Replace upgrade strategy on AWS, it should apply globalPS managed label to Machines",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			objects: []client.Object{
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machineset",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
					Spec: capiv1.MachineSetSpec{
						Template: capiv1.MachineTemplateSpec{
							Spec: capiv1.MachineSpec{
								InfrastructureRef: capiv1.ContractVersionedObjectReference{
									Kind:     "AWSMachineTemplate",
									APIGroup: "infrastructure.cluster.x-k8s.io",
									Name:     awsMachineTemplateName,
								},
							},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine-1",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectLabel: true,
		},
		{
			name: "When using Replace upgrade strategy on Azure, it should apply globalPS managed label to Machines",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			objects: []client.Object{
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine-1",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				&capiv1.MachineDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-nodepool",
						Namespace: controlPlaneNamespace,
					},
				},
			},
			reconcile: func(t *testing.T, capi *CAPI) error {
				md := &capiv1.MachineDeployment{}
				if err := capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlPlaneNamespace, Name: "test-nodepool"}, md); err != nil {
					return err
				}
				template := &capiazure.AzureMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-azure-template",
						Namespace: controlPlaneNamespace,
					},
				}
				log := ctrl.LoggerFrom(t.Context())
				return capi.reconcileMachineDeployment(t.Context(), log, md, template)
			},
			expectLabel: true,
		},
		{
			name: "When using InPlace upgrade strategy on AWS, it should not apply globalPS managed label to Machines",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeInPlace,
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSNodePoolPlatform{
							AMI: "an-ami",
						},
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region:                      "",
							CloudProviderConfig:         &hyperv1.AWSCloudProviderConfig{},
							ServiceEndpoints:            []hyperv1.AWSServiceEndpoint{},
							RolesRef:                    hyperv1.AWSRolesRef{},
							ResourceTags:                []hyperv1.AWSResourceTag{},
							EndpointAccess:              "",
							AdditionalAllowedPrincipals: []string{},
							MultiArch:                   false,
						},
					},
				},
			},
			objects: []client.Object{
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machineset",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
					Spec: capiv1.MachineSetSpec{
						Template: capiv1.MachineTemplateSpec{
							Spec: capiv1.MachineSpec{
								InfrastructureRef: capiv1.ContractVersionedObjectReference{
									Kind:     "AWSMachineTemplate",
									APIGroup: "infrastructure.cluster.x-k8s.io",
									Name:     awsMachineTemplateName,
								},
							},
						},
					},
				},
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine-1",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				&capiaws.AWSMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsMachineTemplateName,
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
		},
		{
			name: "When using Replace upgrade strategy on KubeVirt, it should not apply globalPS managed label to Machines",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					Management: hyperv1.NodePoolManagement{
						UpgradeType: hyperv1.UpgradeTypeReplace,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: &maxUnavailable,
								MaxSurge:       &maxSurge,
							},
						},
					},
					Replicas: ptr.To[int32](3),
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "test-namespace"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			objects: []client.Object{
				&capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-machine-1",
						Namespace: controlPlaneNamespace,
						Annotations: map[string]string{
							nodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
				&capiv1.MachineDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-nodepool",
						Namespace: controlPlaneNamespace,
					},
				},
			},
			// Call reconcileMachineDeployment directly to test the label logic
			// without requiring full KubeVirt machine template setup.
			reconcile: func(t *testing.T, capi *CAPI) error {
				md := &capiv1.MachineDeployment{}
				if err := capi.Client.Get(t.Context(), client.ObjectKey{Namespace: controlPlaneNamespace, Name: "test-nodepool"}, md); err != nil {
					return err
				}
				kvTemplate := &capikubevirt.KubevirtMachineTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-kv-template",
						Namespace: controlPlaneNamespace,
					},
				}
				log := ctrl.LoggerFrom(t.Context())
				return capi.reconcileMachineDeployment(t.Context(), log, md, kvTemplate)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			objects := append([]client.Object{tt.nodePool}, tt.objects...)
			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				Build()

			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                c,
						hostedCluster:         tt.hostedCluster,
						nodePool:              tt.nodePool,
						controlplaneNamespace: controlPlaneNamespace,
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: "target-version",
									},
								},
							},
						},
					},
					cpoCapabilities:        &CPOCapabilities{},
					CreateOrUpdateProvider: upsert.New(false),
				},
				capiClusterName: capiClusterName,
				ApplyProvider:   upsert.NewApplyProvider(false),
			}

			reconcile := tt.reconcile
			if reconcile == nil {
				reconcile = func(t *testing.T, capi *CAPI) error {
					return capi.Reconcile(t.Context())
				}
			}
			err := reconcile(t, capi)
			g.Expect(err).NotTo(HaveOccurred())

			globalPSManagedLabelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, globalPSNodeLabel)
			machineList := &capiv1.MachineList{}
			err = capi.Client.List(t.Context(), machineList, client.InNamespace(controlPlaneNamespace))
			g.Expect(err).NotTo(HaveOccurred())
			foundOwnedMachine := false
			for _, m := range machineList.Items {
				if m.Annotations[nodePoolAnnotation] == client.ObjectKeyFromObject(tt.nodePool).String() {
					foundOwnedMachine = true
					if tt.expectLabel {
						g.Expect(m.Labels).To(HaveKeyWithValue(globalPSManagedLabelKey, "true"),
							"Machine %s should have the globalPS managed label", m.Name)
					} else {
						g.Expect(m.Labels).ToNot(HaveKey(globalPSManagedLabelKey),
							"Machine %s should NOT have the globalPS managed label", m.Name)
					}
				}
			}
			g.Expect(foundOwnedMachine).To(BeTrue(), "expected at least one Machine owned by NodePool %s", client.ObjectKeyFromObject(tt.nodePool).String())
		})
	}
}

func TestPause(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nodepool",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "test-cluster",
			Replicas:    ptr.To[int32](3),
			Management: hyperv1.NodePoolManagement{
				UpgradeType: hyperv1.UpgradeTypeReplace,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSNodePoolPlatform{},
			},
		},
	}

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	controlPlaneNamespace := "test-cp-namespace"

	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	capi := &CAPI{
		Token: &Token{
			ConfigGenerator: &ConfigGenerator{
				nodePool:              nodePool,
				hostedCluster:         hostedCluster,
				controlplaneNamespace: controlPlaneNamespace,
				Client:                c,
			},
		},
		capiClusterName: "test-cluster",
	}

	// Create MachineDeployment and MachineSet.
	md := capi.machineDeployment()
	err := capi.Client.Create(t.Context(), md)
	g.Expect(err).NotTo(HaveOccurred())

	ms := capi.machineSet()
	err = capi.Client.Create(t.Context(), ms)
	g.Expect(err).NotTo(HaveOccurred())

	// Test Pause
	err = capi.Pause(t.Context())
	g.Expect(err).NotTo(HaveOccurred())

	// Verify MachineDeployment is paused.
	err = capi.Client.Get(t.Context(), client.ObjectKeyFromObject(md), md)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(md.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))

	// Verify MachineSet is paused
	err = capi.Client.Get(t.Context(), client.ObjectKeyFromObject(ms), ms)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ms.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))
}

func TestSetMachineDeploymentMetadata(t *testing.T) {
	testCases := []struct {
		name                string
		nodePool            *hyperv1.NodePool
		machineDeployment   *capiv1.MachineDeployment
		capiClusterName     string
		expectAnnotationKey string
		expectAnnotationVal string
		expectLabelKey      string
		expectLabelVal      string
		expectPausedRemoved bool
	}{
		{
			name: "When MachineDeployment has nil annotations and labels, it should initialize them and set nodePool annotation and cluster label",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nodepool",
					Namespace: "my-ns",
				},
			},
			machineDeployment: &capiv1.MachineDeployment{},
			capiClusterName:   "my-cluster",
		},
		{
			name: "When MachineDeployment has existing annotations including paused, it should remove paused annotation and set nodePool annotation",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-nodepool",
					Namespace: "my-ns",
				},
			},
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						capiv1.PausedAnnotation: "true",
						"existing-key":          "existing-value",
					},
					Labels: map[string]string{
						"existing-label": "val",
					},
				},
			},
			capiClusterName:     "my-cluster",
			expectPausedRemoved: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool: tc.nodePool,
					},
				},
				capiClusterName: tc.capiClusterName,
			}

			capi.setMachineDeploymentMetadata(tc.machineDeployment, tc.capiClusterName)

			g.Expect(tc.machineDeployment.Annotations).To(HaveKeyWithValue(
				nodePoolAnnotation, client.ObjectKeyFromObject(tc.nodePool).String()))
			g.Expect(tc.machineDeployment.Annotations).ToNot(HaveKey(capiv1.PausedAnnotation))
			g.Expect(tc.machineDeployment.Labels).To(HaveKeyWithValue(
				capiv1.ClusterNameLabel, tc.capiClusterName))
		})
	}
}

func TestSetMachineDeploymentFailureDomain(t *testing.T) {
	testCases := []struct {
		name                  string
		nodePool              *hyperv1.NodePool
		expectedFailureDomain string
	}{
		{
			name: "When platform is OpenStack with AvailabilityZone set, it should set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackNodePoolPlatform{
							AvailabilityZone: "az-1",
						},
					},
				},
			},
			expectedFailureDomain: "az-1",
		},
		{
			name: "When platform is OpenStack with empty AvailabilityZone, it should not set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackNodePoolPlatform{
							AvailabilityZone: "",
						},
					},
				},
			},
			expectedFailureDomain: "",
		},
		{
			name: "When platform is GCP with Zone set, it should set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							Zone: "us-central1-a",
						},
					},
				},
			},
			expectedFailureDomain: "us-central1-a",
		},
		{
			name: "When platform is GCP with empty Zone, it should not set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPNodePoolPlatform{
							Zone: "",
						},
					},
				},
			},
			expectedFailureDomain: "",
		},
		{
			name: "When platform is AWS, it should not set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			expectedFailureDomain: "",
		},
		{
			name: "When platform is OpenStack but spec is nil, it should not set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.OpenStackPlatform,
					},
				},
			},
			expectedFailureDomain: "",
		},
		{
			name: "When platform is GCP but spec is nil, it should not set failure domain",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.GCPPlatform,
					},
				},
			},
			expectedFailureDomain: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			md := &capiv1.MachineDeployment{}
			setMachineDeploymentFailureDomain(tc.nodePool, md)
			g.Expect(md.Spec.Template.Spec.FailureDomain).To(Equal(tc.expectedFailureDomain))
		})
	}
}

func TestPropagateVersionAndTemplate(t *testing.T) {
	testCases := []struct {
		name                 string
		currentBootstrapName string
		currentVersion       string
		templateName         string
		currentInfraRefName  string
		useDifferentUserData bool
		expectedInfraRefName string
	}{
		{
			name:                 "When user data secret name differs from current bootstrap, it should propagate version",
			currentBootstrapName: "old-userdata",
			currentVersion:       "4.16.0",
			templateName:         "template-1",
			currentInfraRefName:  "template-1",
			useDifferentUserData: true,
			expectedInfraRefName: "template-1",
		},
		{
			name:                 "When machine template name differs from infra ref, it should propagate template",
			currentBootstrapName: "", // will be set to match computed name
			currentVersion:       "4.17.0",
			templateName:         "new-template",
			currentInfraRefName:  "old-template",
			expectedInfraRefName: "new-template",
		},
		{
			name:                 "When both user data and template differ, it should propagate both",
			currentBootstrapName: "old-userdata",
			currentVersion:       "4.16.0",
			templateName:         "new-template",
			currentInfraRefName:  "old-template",
			useDifferentUserData: true,
			expectedInfraRefName: "new-template",
		},
		{
			name:                 "When nothing differs, it should not update",
			currentBootstrapName: "", // will be set to match computed name
			currentVersion:       "4.17.0",
			templateName:         "same-template",
			currentInfraRefName:  "same-template",
			expectedInfraRefName: "same-template",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
			}

			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool:              nodePool,
						controlplaneNamespace: "cp-ns",
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: "4.17.0",
									},
								},
							},
						},
					},
				},
			}

			// Compute the actual UserDataSecret name from the CAPI struct.
			computedUserDataName := capi.UserDataSecret().Name

			// If the test wants the current bootstrap to match, use the computed name.
			bootstrapName := tc.currentBootstrapName
			if !tc.useDifferentUserData && bootstrapName == "" {
				bootstrapName = computedUserDataName
			}

			md := &capiv1.MachineDeployment{
				Spec: capiv1.MachineDeploymentSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							Bootstrap: capiv1.Bootstrap{
								DataSecretName: ptr.To(bootstrapName),
							},
							InfrastructureRef: capiv1.ContractVersionedObjectReference{
								Name: tc.currentInfraRefName,
							},
							Version: tc.currentVersion,
						},
					},
				},
			}

			templateCR := &capiaws.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: tc.templateName,
				},
			}

			capi.propagateVersionAndTemplate(logr.Discard(), md, templateCR)
			g.Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(Equal(tc.expectedInfraRefName))

			if tc.useDifferentUserData {
				g.Expect(*md.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal(computedUserDataName))
				g.Expect(md.Spec.Template.Spec.Version).To(Equal("4.17.0"))
			}
		})
	}
}

func TestReconcileMachineDeploymentStatus(t *testing.T) {
	testCases := []struct {
		name                         string
		machineDeployment            *capiv1.MachineDeployment
		extraObjects                 []client.Object
		nodePoolVersion              string
		nodePoolAnnotations          map[string]string
		targetVersion                string
		expectedVersion              string
		expectedReplicas             int32
		expectedConfigAnnotation     bool
		expectedTemplateAnnotation   bool
		expectedReadyConditionStatus corev1.ConditionStatus
		expectedReadyConditionSet    bool
	}{
		{
			name: "When MachineDeployment is complete, it should update nodePool version and annotations",
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "cp-ns", Generation: 1},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To[int32](3),
					UpToDateReplicas:   ptr.To[int32](3),
					ReadyReplicas:      ptr.To[int32](3),
					AvailableReplicas:  ptr.To[int32](3),
					ObservedGeneration: 1,
				},
			},
			extraObjects: []client.Object{
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ms",
						Namespace: "cp-ns",
						Labels:    map[string]string{capiv1.MachineDeploymentNameLabel: "test-md"},
					},
					Spec: capiv1.MachineSetSpec{
						Template: capiv1.MachineTemplateSpec{
							Spec: capiv1.MachineSpec{
								InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "template-name"},
							},
						},
					},
				},
			},
			nodePoolVersion:            "",
			nodePoolAnnotations:        map[string]string{},
			targetVersion:              "4.17.0",
			expectedVersion:            "4.17.0",
			expectedReplicas:           3,
			expectedConfigAnnotation:   true,
			expectedTemplateAnnotation: true,
		},
		{
			name: "When MachineDeployment is not complete, it should only update replicas",
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To[int32](3),
					UpToDateReplicas:   ptr.To[int32](1),
					ReadyReplicas:      ptr.To[int32](1),
					AvailableReplicas:  ptr.To[int32](2),
					ObservedGeneration: 1,
				},
			},
			nodePoolVersion:            "4.16.0",
			nodePoolAnnotations:        map[string]string{},
			targetVersion:              "4.17.0",
			expectedVersion:            "4.16.0",
			expectedReplicas:           2,
			expectedConfigAnnotation:   false,
			expectedTemplateAnnotation: false,
		},
		{
			name: "When MachineDeployment has Ready condition, it should propagate it to nodePool",
			machineDeployment: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: capiv1.MachineDeploymentStatus{
					AvailableReplicas: ptr.To[int32](2),
					Conditions: []metav1.Condition{
						{
							Type:    string(capiv1.MachinesReadyCondition),
							Status:  metav1.ConditionTrue,
							Reason:  "SomeReason",
							Message: "all good",
						},
					},
				},
			},
			nodePoolVersion:              "4.16.0",
			nodePoolAnnotations:          map[string]string{},
			targetVersion:                "4.17.0",
			expectedVersion:              "4.16.0",
			expectedReplicas:             2,
			expectedReadyConditionSet:    true,
			expectedReadyConditionStatus: corev1.ConditionTrue,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-np",
					Namespace:   "test-ns",
					Annotations: tc.nodePoolAnnotations,
				},
				Status: hyperv1.NodePoolStatus{
					Version: tc.nodePoolVersion,
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if len(tc.extraObjects) > 0 {
				clientBuilder = clientBuilder.WithObjects(tc.extraObjects...)
			}
			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:                clientBuilder.Build(),
						nodePool:              nodePool,
						controlplaneNamespace: "cp-ns",
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: tc.targetVersion,
									},
								},
							},
						},
					},
				},
			}

			templateCR := &capiaws.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "template-name",
				},
			}

			capi.reconcileMachineDeploymentStatus(context.Background(), logr.Discard(), tc.machineDeployment, templateCR)

			g.Expect(nodePool.Status.Replicas).To(Equal(tc.expectedReplicas))
			g.Expect(nodePool.Status.Version).To(Equal(tc.expectedVersion))

			if tc.expectedConfigAnnotation {
				g.Expect(nodePool.Annotations).To(HaveKey(nodePoolAnnotationCurrentConfig))
			}
			if tc.expectedTemplateAnnotation {
				g.Expect(nodePool.Annotations).To(HaveKeyWithValue(
					nodePoolAnnotationPlatformMachineTemplate, "template-name"))
			}

			if tc.expectedReadyConditionSet {
				readyCond := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReadyConditionType)
				g.Expect(readyCond).ToNot(BeNil())
				g.Expect(readyCond.Status).To(Equal(tc.expectedReadyConditionStatus))
			}
		})
	}
}

func TestPropagateLabelsAndTaintsToMachines(t *testing.T) {
	testCases := []struct {
		name             string
		nodePool         *hyperv1.NodePool
		machines         []capiv1.Machine
		expectLabels     map[string]string
		expectTaintsJSON string
	}{
		{
			name: "When NodePool has labels and taints on AWS platform, it should propagate them to owned machines with globalPS label",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					NodeLabels: map[string]string{
						"custom-label": "custom-value",
					},
					Taints: []hyperv1.Taint{
						{Key: "key1", Value: "val1", Effect: "NoSchedule"},
					},
				},
			},
			machines: []capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machine-1",
						Namespace: "cp-ns",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-ns/test-np",
						},
					},
				},
			},
			expectLabels: map[string]string{
				"managed.hypershift.openshift.io.custom-label":                                      "custom-value",
				"managed.hypershift.openshift.io.hypershift.openshift.io/nodepool-globalps-enabled": "true",
			},
			expectTaintsJSON: `[{"key":"key1","value":"val1","effect":"NoSchedule"}]`,
		},
		{
			name: "When NodePool is on KubeVirt platform, it should propagate labels but not globalPS label",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
					NodeLabels: map[string]string{
						"my-label": "my-value",
					},
				},
			},
			machines: []capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machine-1",
						Namespace: "cp-ns",
						Annotations: map[string]string{
							nodePoolAnnotation: "test-ns/test-np",
						},
					},
				},
			},
			expectLabels: map[string]string{
				"managed.hypershift.openshift.io.my-label": "my-value",
			},
		},
		{
			name: "When machine does not belong to the NodePool, it should not be modified",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-np",
					Namespace: "test-ns",
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
					NodeLabels: map[string]string{
						"custom-label": "custom-value",
					},
				},
			},
			machines: []capiv1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-machine",
						Namespace: "cp-ns",
						Annotations: map[string]string{
							nodePoolAnnotation: "other-ns/other-np",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var objects []client.Object
			for i := range tc.machines {
				objects = append(objects, &tc.machines[i])
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()

			capi := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						Client:   c,
						nodePool: tc.nodePool,
					},
				},
			}

			md := &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cp-ns",
				},
			}

			err := capi.propagateLabelsAndTaintsToMachines(t.Context(), logr.Discard(), md)
			g.Expect(err).ToNot(HaveOccurred())

			// Check machines that belong to the NodePool.
			machineList := &capiv1.MachineList{}
			g.Expect(c.List(t.Context(), machineList, client.InNamespace("cp-ns"))).To(Succeed())

			npKey := client.ObjectKeyFromObject(tc.nodePool).String()
			for _, m := range machineList.Items {
				if m.Annotations[nodePoolAnnotation] != npKey {
					// Machine doesn't belong to this NodePool - should have no managed labels.
					for k := range m.Labels {
						g.Expect(k).ToNot(HavePrefix(labelManagedPrefix))
					}
					continue
				}

				for k, v := range tc.expectLabels {
					g.Expect(m.Labels).To(HaveKeyWithValue(k, v))
				}

				if tc.expectTaintsJSON != "" {
					g.Expect(m.Annotations).To(HaveKeyWithValue(nodePoolAnnotationTaints, tc.expectTaintsJSON))
				}
			}
		})
	}
}

// TestPauseUnpauseCycle is a regression test for the interaction between pausing
// NodePools and the Cluster Autoscaler (OCPBUGS-78152 / CNTRLPLANE-3040).
//
// It verifies that:
//   - When a MachineDeployment/MachineSet is paused, the pause annotation is set
//   - When reconcileMachineDeployment/reconcileMachineSet runs after unpause, the
//     pause annotation is removed and autoscaler annotations are preserved
//   - setMachineDeploymentReplicas clamps replicas within [min, max] bounds regardless
//     of external modifications (e.g. CAS decrementing replicas while paused)
func TestPauseUnpauseCycle(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		upgradeType       hyperv1.UpgradeType
		platformType      hyperv1.PlatformType
		initialReplicas   int32
		autoscalingMin    int32
		autoscalingMax    int32
		replicasAtUnpause int32
		expectedReplicas  int32
	}{
		{
			name:              "When a paused MachineDeployment is unpaused it should remove the pause annotation and preserve replicas",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    1,
			autoscalingMax:    4,
			replicasAtUnpause: 4,
			expectedReplicas:  4,
		},
		{
			name:              "When a paused MachineDeployment has replicas decremented to min it should keep replicas at min on unpause",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    1,
			autoscalingMax:    4,
			replicasAtUnpause: 1,
			expectedReplicas:  1,
		},
		{
			name:              "When a paused MachineDeployment has replicas decremented below min it should clamp to min on unpause",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    2,
			autoscalingMax:    4,
			replicasAtUnpause: 1,
			expectedReplicas:  2,
		},
		{
			name:              "When a paused MachineDeployment has replicas incremented above max it should clamp to max on unpause",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    1,
			autoscalingMax:    4,
			replicasAtUnpause: 6,
			expectedReplicas:  4,
		},
		{
			name:              "When a paused MachineDeployment on AWS with min 0 it should allow scale to zero on unpause",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    0,
			autoscalingMax:    4,
			replicasAtUnpause: 0,
			expectedReplicas:  0,
		},
		{
			name:              "When a paused MachineDeployment on non-AWS with min 0 it should clamp to effective min 1 on unpause",
			upgradeType:       hyperv1.UpgradeTypeReplace,
			platformType:      hyperv1.KubevirtPlatform,
			initialReplicas:   4,
			autoscalingMin:    0,
			autoscalingMax:    4,
			replicasAtUnpause: 0,
			expectedReplicas:  1,
		},
		{
			name:              "When a paused MachineSet is unpaused it should remove the pause annotation and preserve replicas",
			upgradeType:       hyperv1.UpgradeTypeInPlace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    1,
			autoscalingMax:    4,
			replicasAtUnpause: 4,
			expectedReplicas:  4,
		},
		{
			name:              "When a paused MachineSet has replicas decremented below min it should clamp to min on unpause",
			upgradeType:       hyperv1.UpgradeTypeInPlace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    2,
			autoscalingMax:    4,
			replicasAtUnpause: 1,
			expectedReplicas:  2,
		},
		{
			name:              "When a paused MachineSet has replicas incremented above max it should clamp to max on unpause",
			upgradeType:       hyperv1.UpgradeTypeInPlace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    1,
			autoscalingMax:    4,
			replicasAtUnpause: 6,
			expectedReplicas:  4,
		},
		{
			name:              "When a paused MachineSet on AWS with min 0 it should allow scale to zero on unpause",
			upgradeType:       hyperv1.UpgradeTypeInPlace,
			platformType:      hyperv1.AWSPlatform,
			initialReplicas:   4,
			autoscalingMin:    0,
			autoscalingMax:    4,
			replicasAtUnpause: 0,
			expectedReplicas:  0,
		},
		{
			name:              "When a paused MachineSet on non-AWS with min 0 it should clamp to effective min 1 on unpause",
			upgradeType:       hyperv1.UpgradeTypeInPlace,
			platformType:      hyperv1.KubevirtPlatform,
			initialReplicas:   4,
			autoscalingMin:    0,
			autoscalingMax:    4,
			replicasAtUnpause: 0,
			expectedReplicas:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
					AutoScaling: &hyperv1.NodePoolAutoScaling{
						Min: ptr.To[int32](tc.autoscalingMin),
						Max: tc.autoscalingMax,
					},
					Management: hyperv1.NodePoolManagement{
						UpgradeType: tc.upgradeType,
						Replace: &hyperv1.ReplaceUpgrade{
							Strategy: hyperv1.UpgradeStrategyRollingUpdate,
							RollingUpdate: &hyperv1.RollingUpdate{
								MaxUnavailable: ptr.To(intstr.FromInt(0)),
								MaxSurge:       ptr.To(intstr.FromInt(1)),
							},
						},
					},
					Platform: hyperv1.NodePoolPlatform{
						Type: tc.platformType,
					},
				},
			}
			if tc.platformType == hyperv1.AWSPlatform {
				nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{AMI: "test-ami"}
			}

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}
			if tc.platformType == hyperv1.AWSPlatform {
				hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{}
			}

			controlPlaneNamespace := "test-cp-namespace"
			c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			capiObj := &CAPI{
				Token: &Token{
					ConfigGenerator: &ConfigGenerator{
						nodePool:              nodePool,
						hostedCluster:         hostedCluster,
						controlplaneNamespace: controlPlaneNamespace,
						Client:                c,
						rolloutConfig: &rolloutConfig{
							releaseImage: &releaseinfo.ReleaseImage{
								ImageStream: &imageapi.ImageStream{
									ObjectMeta: metav1.ObjectMeta{
										Name: "target-version",
									},
								},
							},
						},
					},
					cpoCapabilities:        &CPOCapabilities{},
					CreateOrUpdateProvider: upsert.New(false),
				},
				capiClusterName: "test-cluster",
			}

			// Create MachineDeployment and MachineSet with initial replicas and autoscaler annotations.
			md := capiObj.machineDeployment()
			md.Spec.Replicas = ptr.To[int32](tc.initialReplicas)
			md.Annotations = map[string]string{
				autoscalerMinAnnotation: fmt.Sprintf("%d", tc.autoscalingMin),
				autoscalerMaxAnnotation: fmt.Sprintf("%d", tc.autoscalingMax),
			}
			g.Expect(c.Create(t.Context(), md)).To(Succeed())

			ms := capiObj.machineSet()
			ms.Spec.Replicas = ptr.To[int32](tc.initialReplicas)
			ms.Annotations = map[string]string{
				autoscalerMinAnnotation: fmt.Sprintf("%d", tc.autoscalingMin),
				autoscalerMaxAnnotation: fmt.Sprintf("%d", tc.autoscalingMax),
			}
			g.Expect(c.Create(t.Context(), ms)).To(Succeed())

			// Step 1: Pause.
			g.Expect(capiObj.Pause(t.Context())).To(Succeed())

			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(md), md)).To(Succeed())
			g.Expect(md.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))

			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(ms), ms)).To(Succeed())
			g.Expect(ms.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))

			// Step 2: Simulate replica drift while paused (e.g. CAS decrementing via Scale subresource).
			if tc.upgradeType == hyperv1.UpgradeTypeReplace {
				md.Spec.Replicas = ptr.To[int32](tc.replicasAtUnpause)
				g.Expect(c.Update(t.Context(), md)).To(Succeed())
			} else {
				ms.Spec.Replicas = ptr.To[int32](tc.replicasAtUnpause)
				g.Expect(c.Update(t.Context(), ms)).To(Succeed())
			}

			// Step 3: Unpause by calling the reconcile functions (simulates the NodePool controller
			// reconciling after PausedUntil expires, which calls capi.Reconcile()).
			log := ctrl.LoggerFrom(t.Context())
			template := &capiaws.AWSMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-template",
					Namespace: controlPlaneNamespace,
				},
			}

			// When the platform matches scaleFromZeroPlatform, effectiveMin=0 is allowed.
			// For platforms without scale-from-zero support, effectiveMin is clamped to 1.
			capiObj.scaleFromZeroPlatform = tc.platformType
			expectedMinAnnotation := tc.autoscalingMin
			if tc.autoscalingMin == 0 && tc.platformType == hyperv1.KubevirtPlatform {
				// Simulate no scale-from-zero provider configured for KubeVirt
				capiObj.scaleFromZeroPlatform = ""
				expectedMinAnnotation = 1
			}

			if tc.upgradeType == hyperv1.UpgradeTypeReplace {
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(md), md)).To(Succeed())
				g.Expect(capiObj.reconcileMachineDeployment(t.Context(), log, md, template)).To(Succeed())

				// Verify pause annotation removed.
				g.Expect(md.Annotations).NotTo(HaveKey(capiv1.PausedAnnotation))
				// Verify replicas clamped within autoscaler bounds.
				g.Expect(*md.Spec.Replicas).To(Equal(tc.expectedReplicas))
				// Verify autoscaler annotations preserved.
				g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, fmt.Sprintf("%d", expectedMinAnnotation)))
				g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, fmt.Sprintf("%d", tc.autoscalingMax)))
			} else {
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(ms), ms)).To(Succeed())
				g.Expect(capiObj.reconcileMachineSet(t.Context(), ms, template)).To(Succeed())

				// Verify pause annotation removed.
				g.Expect(ms.Annotations).NotTo(HaveKey(capiv1.PausedAnnotation))
				// Verify replicas clamped within autoscaler bounds.
				g.Expect(*ms.Spec.Replicas).To(Equal(tc.expectedReplicas))
				// Verify autoscaler annotations preserved.
				g.Expect(ms.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, fmt.Sprintf("%d", expectedMinAnnotation)))
				g.Expect(ms.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, fmt.Sprintf("%d", tc.autoscalingMax)))
			}
		})
	}
}

func TestNewCAPI(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name             string
		token            *Token
		capiClusterName  string
		expectedErrorMsg string
	}{
		{
			name:             "when token is nil it should fail",
			token:            nil,
			capiClusterName:  "test-cluster",
			expectedErrorMsg: "token can not be nil",
		},
		{
			name: "when capiClusterName is empty it should fail",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{},
			},
			capiClusterName:  "",
			expectedErrorMsg: "capiClusterName can not be empty",
		},
		{
			name: "succeeds with valid parameters",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{},
			},
			capiClusterName:  "test-cluster",
			expectedErrorMsg: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			capi, err := newCAPI(tc.token, tc.capiClusterName)
			if tc.expectedErrorMsg != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedErrorMsg))
				g.Expect(capi).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(capi).NotTo(BeNil())
				g.Expect(capi.Token).To(BeEquivalentTo(tc.token))
				g.Expect(capi.capiClusterName).To(Equal(tc.capiClusterName))
			}
		})
	}
}

func TestMachineDeploymentComplete(t *testing.T) {
	two := int32(2)

	testCases := []struct {
		name                  string
		md                    *capiv1.MachineDeployment
		targetMachineTemplate string
		extraObjects          []client.Object
		expected              bool
	}{
		{
			name: "When all fields match spec replicas and generation it should return true",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: &two},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To(int32(2)),
					UpToDateReplicas:   ptr.To(int32(2)),
					AvailableReplicas:  ptr.To(int32(2)),
					ObservedGeneration: 2,
				},
			},
			expected: true,
		},
		{
			name: "When availableReplicas is zero it should return false",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: &two},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To[int32](2),
					UpToDateReplicas:   ptr.To[int32](2),
					AvailableReplicas:  ptr.To[int32](0),
					ObservedGeneration: 2,
				},
			},
			expected: false,
		},
		{
			name: "When v1beta1 looks complete but v1beta2 availableReplicas disagrees it should return false",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: &two},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To(int32(2)),
					UpToDateReplicas:   ptr.To(int32(2)),
					AvailableReplicas:  ptr.To(int32(1)),
					ObservedGeneration: 2,
				},
			},
			expected: false,
		},
		{
			name: "When v1beta1 is not complete it should return false without checking v1beta2",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: &two},
				Status:     capiv1.MachineDeploymentStatus{Replicas: ptr.To(int32(3)), AvailableReplicas: ptr.To(int32(2)), ObservedGeneration: 2},
			},
			expected: false,
		},
		{
			name: "When v1beta2 status is nil it should fail",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: &two},
				Status:     capiv1.MachineDeploymentStatus{Replicas: ptr.To(int32(2)), AvailableReplicas: ptr.To(int32(2)), ObservedGeneration: 1},
			},
			expected: false,
		},
		{
			name: "When status looks complete but no MachineSet has target template it should return false",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "cp-ns", Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: ptr.To[int32](2)},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To[int32](2),
					UpToDateReplicas:   ptr.To[int32](2),
					AvailableReplicas:  ptr.To[int32](2),
					ObservedGeneration: 2,
				},
			},
			targetMachineTemplate: "new-template",
			extraObjects: []client.Object{
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ms-old",
						Namespace: "cp-ns",
						Labels:    map[string]string{capiv1.MachineDeploymentNameLabel: "test-md"},
					},
					Spec: capiv1.MachineSetSpec{
						Template: capiv1.MachineTemplateSpec{
							Spec: capiv1.MachineSpec{
								InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "old-template"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When status is complete and MachineSet has target template it should return true",
			md: &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "test-md", Namespace: "cp-ns", Generation: 2},
				Spec:       capiv1.MachineDeploymentSpec{Replicas: ptr.To[int32](2)},
				Status: capiv1.MachineDeploymentStatus{
					Replicas:           ptr.To[int32](2),
					UpToDateReplicas:   ptr.To[int32](2),
					AvailableReplicas:  ptr.To[int32](2),
					ObservedGeneration: 2,
				},
			},
			targetMachineTemplate: "new-template",
			extraObjects: []client.Object{
				&capiv1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ms-new",
						Namespace: "cp-ns",
						Labels:    map[string]string{capiv1.MachineDeploymentNameLabel: "test-md"},
					},
					Spec: capiv1.MachineSetSpec{
						Template: capiv1.MachineTemplateSpec{
							Spec: capiv1.MachineSpec{
								InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "new-template"},
							},
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if len(tc.extraObjects) > 0 {
				clientBuilder = clientBuilder.WithObjects(tc.extraObjects...)
			}
			fakeClient := clientBuilder.Build()
			g.Expect(MachineDeploymentComplete(context.Background(), fakeClient, tc.md, tc.targetMachineTemplate)).To(Equal(tc.expected))
		})
	}
}
