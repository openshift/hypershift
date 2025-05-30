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

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/stdr"
)

func TestMachineSizesCache(t *testing.T) {
	g := NewGomegaWithT(t)
	sizes := machineSizes{}
	log := stdr.New(nil)

	ms := func(sizeLabel, memorySize string) machinev1beta1.MachineSet {
		result := machinev1beta1.MachineSet{}
		result.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{"hypershift.openshift.io/cluster-size": sizeLabel}
		result.Annotations = map[string]string{
			"machine.openshift.io/memoryMb": memorySize,
			"machine.openshift.io/vCPU":     "2", // not relevant for now
		}
		return result
	}

	listMachineSets := func() (*machinev1beta1.MachineSetList, error) {
		return &machinev1beta1.MachineSetList{
			Items: []machinev1beta1.MachineSet{ms("small", "4096"), ms("medium", "8192"), ms("large", "16384")},
		}, nil
	}
	csc := hostedclustersizing.DefaultSizingConfig()
	csc.Generation = 1
	err := sizes.update(csc, listMachineSets, log)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(sizes.sizesInOrderByMemory()).To(Equal([]string{"small", "medium", "large"}))
	g.Expect(sizes.sizes["medium"].Memory).To(Equal(resource.MustParse("8192Mi")))
	failOnCall := func() (*machinev1beta1.MachineSetList, error) {
		g.Fail("this function should not be called")
		return nil, nil
	}
	// ensure if update is called again with the same csc generation, machinesets are not listed again
	err = sizes.update(csc, failOnCall, log)
	g.Expect(err).ToNot(HaveOccurred())
}

func TestMachineSizesCacheWithSizingConfig(t *testing.T) {
	g := NewGomegaWithT(t)
	log := stdr.New(nil)
	csc := hostedclustersizing.DefaultSizingConfig()
	csc.Generation = 1
	size := resource.MustParse("8Gi")
	for i := range csc.Spec.Sizes {
		csc.Spec.Sizes[i].Capacity = &schedulingv1alpha1.SizeCapacity{
			Memory: ptr.To(size),
		}
		size.Mul(2)
	}
	failOnCall := func() (*machinev1beta1.MachineSetList, error) {
		g.Fail("this function should not be called")
		return nil, nil
	}
	sizes := machineSizes{}
	err := sizes.update(csc, failOnCall, log)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(sizes.sizesInOrderByMemory()).To(Equal([]string{"small", "medium", "large"}))
	g.Expect(sizes.sizes["small"].Memory).To(Equal(resource.MustParse("8Gi")))
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
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			csc := hostedclustersizing.DefaultSizingConfig()
			csc.Generation = 1
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
				sizeCache: machineSizes{
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
			if tc.expectRecommendedSize != "" {
				hc := defaultHC()
				err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(hc), hc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(hc.Annotations[hyperv1.RecommendedClusterSizeAnnotation]).To(Equal(tc.expectRecommendedSize))
			}
		})
	}

}
