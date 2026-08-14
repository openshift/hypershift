package resourcebasedcpautoscaler

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedclustersizing"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	controlplaneautoscalermanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneautoscaler"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// defaultSizingConfigWithCapacity returns a ClusterSizingConfiguration with memory capacity
// information that allows the cache to bypass machine set introspection for testing
func defaultSizingConfigWithCapacity() *schedulingv1alpha1.ClusterSizingConfiguration {
	csc := hostedclustersizing.DefaultSizingConfig()
	csc.Generation = 1

	// Add memory capacity to each size configuration to match test expectations
	sizeMemoryMap := map[string]string{
		"small":  "4Gi",
		"medium": "8Gi",
		"large":  "16Gi",
	}

	for i := range csc.Spec.Sizes {
		size := &csc.Spec.Sizes[i]
		if memoryStr, exists := sizeMemoryMap[size.Name]; exists {
			if size.Capacity == nil {
				size.Capacity = &schedulingv1alpha1.SizeCapacity{}
			}
			memory := resource.MustParse(memoryStr)
			size.Capacity.Memory = &memory
		}
	}

	return csc
}

func TestReconcile(t *testing.T) {

	defaultHC := func() *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{}
		hc.Namespace = "test-ns"
		hc.Name = "test-hc"
		hc.Annotations = map[string]string{
			hyperv1.ResourceBasedControlPlaneAutoscalingAnnotation: "true",
			hyperv1.TopologyAnnotation:                             hyperv1.DedicatedRequestServingComponentsTopology,
		}
		return hc
	}
	defaultVPA := func() *vpaautoscalingv1.VerticalPodAutoscaler {
		cpNamespace := manifests.HostedControlPlaneNamespace(defaultHC().Namespace, defaultHC().Name)
		vpa := controlplaneautoscalermanifests.KubeAPIServerVerticalPodAutoscaler(cpNamespace)
		return vpa
	}
	vpaWithRecommendation := func(qty resource.Quantity) *vpaautoscalingv1.VerticalPodAutoscaler {
		vpa := defaultVPA()
		vpa.Status = vpaautoscalingv1.VerticalPodAutoscalerStatus{
			Conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
			},
			Recommendation: &vpaautoscalingv1.RecommendedPodResources{
				ContainerRecommendations: []vpaautoscalingv1.RecommendedContainerResources{
					{
						ContainerName: "kube-apiserver",
						UncappedTarget: corev1.ResourceList{
							corev1.ResourceMemory: qty,
						},
					},
				},
			},
		}
		return vpa
	}
	vpaWithMissingKASRecommendation := func() *vpaautoscalingv1.VerticalPodAutoscaler {
		vpa := defaultVPA()
		vpa.Status = vpaautoscalingv1.VerticalPodAutoscalerStatus{
			Conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
			},
			Recommendation: &vpaautoscalingv1.RecommendedPodResources{
				// Intentionally omit kube-apiserver container
				ContainerRecommendations: []vpaautoscalingv1.RecommendedContainerResources{
					{
						ContainerName: "some-other-container",
						UncappedTarget: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		}
		return vpa
	}

	tcs := []struct {
		name                  string
		hc                    *hyperv1.HostedCluster
		vpa                   *vpaautoscalingv1.VerticalPodAutoscaler
		expectVPAExists       bool
		expectRecommendedSize string
	}{
		{
			name: "hc not applicable",
			hc: func() *hyperv1.HostedCluster {
				hc := defaultHC()
				hc.Annotations = map[string]string{}
				return hc
			}(),
			vpa:             defaultVPA(),
			expectVPAExists: false,
		},
		{
			name:            "vpa not created yet",
			hc:              defaultHC(),
			expectVPAExists: true,
		},
		{
			name:                  "recommendation available",
			hc:                    defaultHC(),
			vpa:                   vpaWithRecommendation(resource.MustParse("5Gi")),
			expectVPAExists:       true,
			expectRecommendedSize: "medium",
		},
		{
			name:            "recommendation missing kube-apiserver container results in no-op",
			hc:              defaultHC(),
			vpa:             vpaWithMissingKASRecommendation(),
			expectVPAExists: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			csc := defaultSizingConfigWithCapacity()
			objs := []client.Object{
				csc,
				tc.hc,
			}
			if tc.vpa != nil {
				objs = append(objs, tc.vpa)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objs...).Build()
			r := &ControlPlaneAutoscalerController{
				Client: fakeClient,
				sizeCache: machineSizesCache{
					cscGeneration: 1,
					sizes: map[string]machineResources{
						"small": {
							Memory: resource.MustParse("4Gi"),
						},
						"medium": {
							Memory: resource.MustParse("8Gi"),
						},
						"large": {
							Memory: resource.MustParse("16Gi"),
						},
					},
				},
				updateSizeCacheFunc: func(ctx context.Context) error {
					return nil
				},
			}
			req := reconcile.Request{}
			req.Name = tc.hc.Name
			req.Namespace = tc.hc.Namespace
			_, err := r.Reconcile(context.Background(), req)
			g.Expect(err).ToNot(HaveOccurred())
			vpa := defaultVPA()
			err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(vpa), vpa)
			if tc.expectVPAExists {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
			}
			// Check the HostedCluster annotation
			hc := defaultHC()
			err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(hc), hc)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.expectRecommendedSize != "" {
				g.Expect(hc.Annotations[hyperv1.RecommendedClusterSizeAnnotation]).To(Equal(tc.expectRecommendedSize))
			} else {
				// If no recommendation expected, the annotation should be absent or empty
				g.Expect(hc.Annotations[hyperv1.RecommendedClusterSizeAnnotation]).To(BeEmpty())
			}
		})
	}

}

func TestReconcileHostedClusterNotFound(t *testing.T) {
	g := NewGomegaWithT(t)
	// Only the ClusterSizingConfiguration exists; no HostedCluster
	csc := defaultSizingConfigWithCapacity()
	fakeClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(csc).Build()
	r := &ControlPlaneAutoscalerController{
		Client: fakeClient,
		sizeCache: machineSizesCache{
			cscGeneration: 1,
			sizes: map[string]machineResources{
				"small":  {Memory: resource.MustParse("4Gi")},
				"medium": {Memory: resource.MustParse("8Gi")},
				"large":  {Memory: resource.MustParse("16Gi")},
			},
		},
		updateSizeCacheFunc: func(ctx context.Context) error {
			return nil
		},
	}
	req := reconcile.Request{NamespacedName: client.ObjectKey{Namespace: "ns", Name: "missing-hc"}}
	_, err := r.Reconcile(context.Background(), req)
	g.Expect(err).ToNot(HaveOccurred())
}
func TestFindVPACondition(t *testing.T) {
	testCases := []struct {
		name          string
		conditions    []vpaautoscalingv1.VerticalPodAutoscalerCondition
		conditionType vpaautoscalingv1.VerticalPodAutoscalerConditionType
		expectFound   bool
		expectIndex   int // which condition should be returned if found
	}{
		{
			name:          "empty conditions list",
			conditions:    []vpaautoscalingv1.VerticalPodAutoscalerCondition{},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   false,
		},
		{
			name: "condition found - single condition",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
			},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   true,
			expectIndex:   0,
		},
		{
			name: "condition not found - single condition",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
			},
			conditionType: vpaautoscalingv1.LowConfidence,
			expectFound:   false,
		},
		{
			name: "condition found - multiple conditions, target is first",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   vpaautoscalingv1.LowConfidence,
					Status: corev1.ConditionFalse,
				},
			},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   true,
			expectIndex:   0,
		},
		{
			name: "condition found - multiple conditions, target is last",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.LowConfidence,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
			},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   true,
			expectIndex:   1,
		},
		{
			name: "condition not found - multiple conditions",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.LowConfidence,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   vpaautoscalingv1.NoPodsMatched,
					Status: corev1.ConditionFalse,
				},
			},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   false,
		},
		{
			name: "returns first matching condition when duplicates exist",
			conditions: []vpaautoscalingv1.VerticalPodAutoscalerCondition{
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   vpaautoscalingv1.LowConfidence,
					Status: corev1.ConditionFalse,
				},
				{
					Type:   vpaautoscalingv1.RecommendationProvided,
					Status: corev1.ConditionFalse,
				},
			},
			conditionType: vpaautoscalingv1.RecommendationProvided,
			expectFound:   true,
			expectIndex:   0, // Should return the first matching condition
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			result := findVPACondition(tc.conditions, tc.conditionType)

			if tc.expectFound {
				g.Expect(result).ToNot(BeNil(), "expected to find condition but got nil")
				g.Expect(result.Type).To(Equal(tc.conditionType), "returned condition has wrong type")

				// Verify we got the correct condition by checking it's the same pointer as expected
				expectedCondition := &tc.conditions[tc.expectIndex]
				g.Expect(result).To(BeIdenticalTo(expectedCondition), "returned condition is not the expected one")
			} else {
				g.Expect(result).To(BeNil(), "expected nil but found condition")
			}
		})
	}
}
