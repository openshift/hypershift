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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestSetMachineSetReplicas(t *testing.T) {
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineSet                  *capiv1.MachineSet
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
						Min: 1,
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
						Min: 2,
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
						Min: 2,
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
						Min: 2,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineSetReplicas(tc.nodePool, tc.machineSet)
			g.Expect(*tc.machineSet.Spec.Replicas).To(Equal(tc.expectReplicas))
			g.Expect(tc.machineSet.Annotations).To(Equal(tc.expectAutoscalerAnnotations))
		})
	}
}

func TestSetMachineDeploymentReplicas(t *testing.T) {
	testCases := []struct {
		name                        string
		nodePool                    *hyperv1.NodePool
		machineDeployment           *capiv1.MachineDeployment
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
						Min: 1,
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
						Min: 1,
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
						Min: 1,
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
						Min: 1,
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
						Min: 2,
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
						Min: 2,
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
						Min: 2,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			setMachineDeploymentReplicas(tc.nodePool, tc.machineDeployment)
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
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(false),
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
		err := r.Create(context.Background(), preCreatedMachineTemplate)
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
	template, mutateTemplate, machineTemplateSpecJSON, err := capi.machineTemplateBuilders()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(machineTemplateSpecJSON).To(BeIdenticalTo(string(expectedMachineTemplateSpecJSON)))

	// Validate that template and mutateTemplate are able to produce an expected target template.
	_, err = r.CreateOrUpdate(context.Background(), r.Client, template, func() error {
		return mutateTemplate(template)
	})
	g.Expect(err).ToNot(HaveOccurred())

	gotMachineTemplate := &capiaws.AWSMachineTemplate{}
	g.Expect(r.Client.Get(context.Background(), client.ObjectKeyFromObject(expectedMachineTemplate), gotMachineTemplate)).To(Succeed())
	g.Expect(expectedMachineTemplate.Spec).To(BeEquivalentTo(gotMachineTemplate.Spec))
	g.Expect(expectedMachineTemplate.ObjectMeta.Annotations).To(BeEquivalentTo(gotMachineTemplate.ObjectMeta.Annotations))
}

func TestMachineTemplateBuilders(t *testing.T) {
	RunTestMachineTemplateBuilders(t, false)
}

func TestMachineTemplateBuildersPreexisting(t *testing.T) {
	RunTestMachineTemplateBuilders(t, true)
}

func TestCleanupMachineTemplates(t *testing.T) {
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
					InfrastructureRef: corev1.ObjectReference{
						Kind:       gvk.Kind,
						APIVersion: gvk.GroupVersion().String(),
						Name:       template1.Name,
						Namespace:  template1.Namespace,
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

	err = capi.cleanupMachineTemplates(context.Background(), logr.Discard(), nodePool, "test")
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
	g.Expect(r.Client.Create(context.Background(), nodePool)).To(BeNil())

	// MachineTemplate with the expected annotation
	template1 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "template1",
			Namespace:   "test",
			Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()},
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(context.Background(), template1)).To(BeNil())

	// MachineTemplate without the expected annotation
	template2 := &capiaws.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "template2",
			Namespace: "test",
		},
		Spec: capiaws.AWSMachineTemplateSpec{},
	}
	g.Expect(r.Client.Create(context.Background(), template2)).To(BeNil())

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
	g.Expect(r.Client.Create(context.Background(), nodePool)).To(BeNil())

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
			g := NewWithT(t)
			maxUnavailable, err := getInPlaceMaxUnavailable(tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(maxUnavailable).To(Equal(tc.expect))
		})
	}
}

func TestTaintsToJSON(t *testing.T) {
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
		mhc.Spec = capiv1.MachineHealthCheckSpec{
			ClusterName: "cluster",
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					resName: resName,
				},
			},
			UnhealthyConditions: []capiv1.UnhealthyCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
					Timeout: metav1.Duration{
						Duration: time.Duration(8 * time.Minute),
					},
				},
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionUnknown,
					Timeout: metav1.Duration{
						Duration: time.Duration(8 * time.Minute),
					},
				},
			},
			MaxUnhealthy: &defaultMaxUnhealthy,
			NodeStartupTimeout: &metav1.Duration{
				Duration: 20 * time.Minute,
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
	withMaxUnhealthy := func(value string) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			maxUnhealthy := intstr.Parse(value)
			mhc.Spec.MaxUnhealthy = &maxUnhealthy
		}
	}
	withTimeout := func(d time.Duration) func(*capiv1.MachineHealthCheck) {
		return func(mhc *capiv1.MachineHealthCheck) {
			for i := range mhc.Spec.UnhealthyConditions {
				mhc.Spec.UnhealthyConditions[i].Timeout = metav1.Duration{Duration: d}
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
			mhc.Spec.NodeStartupTimeout = &metav1.Duration{Duration: d}
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
			name:     "maxunhealthy override in hc",
			hc:       hostedcluster(withMaxUnhealthyOverride("10%")),
			np:       nodepool(),
			expected: healthcheck(withMaxUnhealthy("10%")),
		},
		{
			name:     "maxunhealthy override in np",
			hc:       hostedcluster(),
			np:       nodepool(withMaxUnhealthyOverride("5")),
			expected: healthcheck(withMaxUnhealthy("5")),
		},
		{
			name:     "maxunhealthy override in both, np takes precedence",
			hc:       hostedcluster(withMaxUnhealthyOverride("10%")),
			np:       nodepool(withMaxUnhealthyOverride("5")),
			expected: healthcheck(withMaxUnhealthy("5")),
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
			err := capi.reconcileMachineHealthCheck(context.Background(), mhc)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(mhc.Spec).To(testutil.MatchExpected(tt.expected.Spec))
		})
	}
}

func TestCAPIReconcile(t *testing.T) {
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)
	// This is the generated name by machineTemplateBuilders.
	// So reconciliation doesn't create a new AWSMachineTemplate but reconcile this one.
	awsMachineTemplateName := "test-nodepool-77a60936"
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
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{
								Kind:       "AWSMachineTemplate",
								APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
								Namespace:  "test-namespace-test-cluster",
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
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{
								Kind:       "AWSMachineTemplate",
								APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
								Namespace:  "test-namespace-test-cluster",
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
						Min: 3,
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
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							InfrastructureRef: corev1.ObjectReference{
								Kind:       "AWSMachineTemplate",
								APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
								Namespace:  "test-namespace-test-cluster",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			}

			if tt.expectedError {
				// TODO(alberto): use WithObjectTracker() / WithInterceptorFuncs() to mock error paths.
			}

			// Make sure the templates are populates in the control plane namespace
			templateList := &capiaws.AWSMachineTemplateList{}
			err := capi.Client.List(context.Background(), templateList, client.InNamespace(controlpaneNamespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(templateList.Items).To(HaveLen(2))

			err = capi.Reconcile(context.Background())
			if tt.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())

				// Check that old machine templates are deleted.
				templateList := &capiaws.AWSMachineTemplateList{}
				err := capi.Client.List(context.Background(), templateList, client.InNamespace(controlpaneNamespace))
				g.Expect(err).NotTo(HaveOccurred())
				// Expect templates which does not match the ref to be deleted.
				g.Expect(templateList.Items).To(HaveLen(1))
				g.Expect(templateList.Items[0].GetName()).To(Equal(awsMachineTemplateName))

				// Check MachineDeployment.
				md := &capiv1.MachineDeployment{}
				err = capi.Client.Get(context.Background(), client.ObjectKey{Namespace: controlpaneNamespace, Name: "test-nodepool"}, md)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(md.Spec.Replicas).To(Equal(ptr.To[int32](3)))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(Equal(awsMachineTemplateName))
				// Check MachineDeployment annotations
				g.Expect(md.Annotations).To(HaveKeyWithValue(nodePoolAnnotation, "test-namespace/test-nodepool"))

				// Check MachineDeployment spec.
				g.Expect(md.Spec.Strategy.Type).To(Equal(capiv1.MachineDeploymentStrategyType("RollingUpdate")))
				g.Expect(md.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue()).To(Equal(0))
				g.Expect(md.Spec.Strategy.RollingUpdate.MaxSurge.IntValue()).To(Equal(1))

				// Check MachineDeployment labels.
				g.Expect(md.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, capiClusterName))

				// Check MachineDeployment template labels.
				g.Expect(md.Spec.Template.Labels).To(HaveKeyWithValue(capiv1.ClusterNameLabel, capiClusterName))

				// Check MachineDeployment annotations labels.
				g.Expect(md.Spec.Template.Annotations).To(HaveKeyWithValue(nodePoolAnnotation, "test-namespace/test-nodepool"))

				// Check MachineDeployment template spec
				g.Expect(md.Spec.Template.Spec.ClusterName).To(Equal(capiClusterName))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.APIVersion).To(Equal("infrastructure.cluster.x-k8s.io/v1beta2"))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Kind).To(Equal("AWSMachineTemplate"))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Namespace).To(Equal(controlpaneNamespace))
				g.Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(Equal(awsMachineTemplateName))

				g.Expect(*md.Spec.Template.Spec.Version).To(Equal("target-version"))
				g.Expect(md.Spec.Template.Spec.NodeDrainTimeout).To(Equal(tt.nodePool.Spec.NodeDrainTimeout))
				g.Expect(md.Spec.Template.Spec.NodeVolumeDetachTimeout).To(Equal(tt.nodePool.Spec.NodeVolumeDetachTimeout))

				// Check Bootstrap DataSecretName.
				g.Expect(md.Spec.Template.Spec.Bootstrap.DataSecretName).NotTo(BeNil())
				g.Expect(*md.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal("user-data-test-nodepool-ac51f7c1"))
				g.Expect(*md.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal(capi.UserDataSecret().GetName()))

				// Check autoscaling annotations.
				if tt.nodePool.Spec.AutoScaling != nil {
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, fmt.Sprintf("%d", tt.nodePool.Spec.AutoScaling.Max)))
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, fmt.Sprintf("%d", tt.nodePool.Spec.AutoScaling.Min)))
				} else {
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMaxAnnotation, "0"))
					g.Expect(md.Annotations).To(HaveKeyWithValue(autoscalerMinAnnotation, "0"))
				}

				// Check MachineHealthCheck
				if tt.nodePool.Spec.Management.AutoRepair {
					mhc := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(context.Background(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, mhc)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(mhc.Spec.ClusterName).To(Equal(capiClusterName))
				} else {
					mhc := &capiv1.MachineHealthCheck{}
					err = capi.Client.Get(context.Background(), client.ObjectKey{Namespace: "test-cp-namespace", Name: tt.nodePool.GetName()}, mhc)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}

				if tt.sameUserData {
					// Get the MachineDeployment.
					md := &capiv1.MachineDeployment{}
					err = capi.Client.Get(context.Background(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, md)
					g.Expect(err).NotTo(HaveOccurred())

					// Update MachineDeployment status to indicate rollout is complete.
					md.Status.Replicas = *tt.nodePool.Spec.Replicas
					md.Status.UpdatedReplicas = *tt.nodePool.Spec.Replicas
					md.Status.ReadyReplicas = *tt.nodePool.Spec.Replicas
					md.Status.AvailableReplicas = *tt.nodePool.Spec.Replicas
					md.Status.ObservedGeneration = md.Generation
					err = capi.Client.Update(context.Background(), md)
					g.Expect(err).NotTo(HaveOccurred())

					md = &capiv1.MachineDeployment{}
					err = capi.Client.Get(context.Background(), client.ObjectKey{Namespace: controlpaneNamespace, Name: tt.nodePool.GetName()}, md)
					g.Expect(err).NotTo(HaveOccurred())

					// Re-run reconcile.
					err = capi.Reconcile(context.Background())
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

func TestPause(t *testing.T) {
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
	err := capi.Client.Create(context.Background(), md)
	g.Expect(err).NotTo(HaveOccurred())

	ms := capi.machineSet()
	err = capi.Client.Create(context.Background(), ms)
	g.Expect(err).NotTo(HaveOccurred())

	// Test Pause
	err = capi.Pause(context.Background())
	g.Expect(err).NotTo(HaveOccurred())

	// Verify MachineDeployment is paused.
	err = capi.Client.Get(context.Background(), client.ObjectKeyFromObject(md), md)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(md.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))

	// Verify MachineSet is paused
	err = capi.Client.Get(context.Background(), client.ObjectKeyFromObject(ms), ms)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ms.Annotations).To(HaveKeyWithValue(capiv1.PausedAnnotation, "true"))
}

func TestNewCAPI(t *testing.T) {
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
