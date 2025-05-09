package resourcebasedcpautoscaler

import (
	"context"
	"fmt"
	"slices"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	controlplaneautoscalermanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneautoscaler"
	"github.com/openshift/hypershift/support/util"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	ControllerName = "ResourceBasedControlPlaneAutoscaler"

	// kubeAPIServerMemorySizeFractionDefault is used to determine whether the VPA recommendation fits within a cluster size.
	// The HostedCluster will be sized to the smallest size for which capacity * kubeAPIServerMemorySizeFractionDefault >= recommendation.
	kubeAPIServerMemorySizeFractionDefault = 0.65

	// hosted cluster namespace annotation on VPA
	hcNamespaceAnnotation = "hypershift.openshift.io/cluster-namespace"

	// hosted cluster name annotation on VPA
	hcNameAnnotation = "hypershift.openshift.io/cluster-name"
)

type ControlPlaneAutoscalerController struct {
	client.Client
	sizeCache machineSizes
}

type machineSizes struct {
	cscGeneration         int64
	kasMemorySizeFraction *resource.Quantity
	sizes                 map[string]machineResources
	m                     sync.Mutex
}

type machineResources struct {
	Memory resource.Quantity
	CPU    resource.Quantity
}

func (s *machineSizes) update(csc *schedulingv1alpha1.ClusterSizingConfiguration, listMachineSets func() (*machinev1beta1.MachineSetList, error), log logr.Logger) error {
	s.m.Lock()
	defer s.m.Unlock()

	if csc.Generation == s.cscGeneration {
		// machine sizes are up to date
		return nil
	}

	log.Info("Updating machine size cache")
	s.kasMemorySizeFraction = csc.Spec.ResourceBasedAutoscaling.KubeAPIServerMemoryFraction

	if sizeMemoryCapacityAvailable(csc) {
		return s.updateSizesFromConfig(csc)
	}

	return s.updateSizesFromMachineSets(csc, listMachineSets)
}

func sizeMemoryCapacityAvailable(csc *schedulingv1alpha1.ClusterSizingConfiguration) bool {
	for _, size := range csc.Spec.Sizes {
		if size.Capacity == nil {
			return false
		}
		if size.Capacity.Memory == nil {
			return false
		}
	}
	return true
}

func (s *machineSizes) updateSizesFromConfig(csc *schedulingv1alpha1.ClusterSizingConfiguration) error {
	sizes := map[string]machineResources{}
	for _, sizeConfig := range csc.Spec.Sizes {
		resources := machineResources{
			Memory: ptr.Deref(sizeConfig.Capacity.Memory, *resource.NewQuantity(0, resource.DecimalSI)),
			CPU:    ptr.Deref(sizeConfig.Capacity.CPU, *resource.NewQuantity(0, resource.DecimalSI)),
		}
		sizes[sizeConfig.Name] = resources
	}
	s.sizes = sizes
	s.cscGeneration = csc.Generation
	return nil
}

func (s *machineSizes) updateSizesFromMachineSets(csc *schedulingv1alpha1.ClusterSizingConfiguration, listMachineSets func() (*machinev1beta1.MachineSetList, error)) error {
	machineSetList, err := listMachineSets()
	if err != nil {
		return err
	}
	sizes := map[string]machineResources{}
	hasAllSizes := func() bool {
		for _, size := range csc.Spec.Sizes {
			if _, hasSize := sizes[size.Name]; !hasSize {
				return false
			}
		}
		return true
	}

	for _, ms := range machineSetList.Items {
		clusterSize := ms.Spec.Template.Spec.ObjectMeta.Labels["hypershift.openshift.io/cluster-size"]
		if clusterSize == "" {
			continue
		}
		// The first machineset that matches a given size label is used as the authoritative source for
		// machine sizes of that label. If instance types and cluster size labels are not consistent, the
		// size stored in this cache will be unpredictable.
		if _, exists := sizes[clusterSize]; exists {
			continue
		}
		memoryInMB, hasMemory := ms.Annotations["machine.openshift.io/memoryMb"]
		if !hasMemory {
			continue
		}
		vCPU, hasCPU := ms.Annotations["machine.openshift.io/vCPU"]
		if !hasCPU {
			continue
		}

		memory, err := resource.ParseQuantity(memoryInMB + "Mi")
		if err != nil {
			continue
		}
		cpu, err := resource.ParseQuantity(vCPU)
		if err != nil {
			continue
		}
		resources := machineResources{
			Memory: memory,
			CPU:    cpu,
		}
		sizes[clusterSize] = resources
		if hasAllSizes() {
			break
		}
	}
	if !hasAllSizes() {
		return fmt.Errorf("failed to introspect all machine sizes with existing machinesets")
	}
	s.sizes = sizes
	s.cscGeneration = csc.Generation
	return nil
}

func (s *machineSizes) kasMemoryFraction() float64 {
	if s.kasMemorySizeFraction != nil {
		return s.kasMemorySizeFraction.AsApproximateFloat64()
	}
	return kubeAPIServerMemorySizeFractionDefault
}

func (s *machineSizes) sizesInOrderByMemory() []string {
	type sizeWithMemory struct {
		size   string
		memory resource.Quantity
	}
	sizesToOrder := make([]sizeWithMemory, 0, len(s.sizes))
	for size, resources := range s.sizes {
		sizesToOrder = append(sizesToOrder, sizeWithMemory{
			size:   size,
			memory: resources.Memory,
		})
	}
	slices.SortFunc(sizesToOrder, func(a, b sizeWithMemory) int {
		return a.memory.Cmp(b.memory)
	})
	sortedSizes := make([]string, len(sizesToOrder))
	for i := range sizesToOrder {
		sortedSizes[i] = sizesToOrder[i].size
	}
	return sortedSizes
}

func (s *machineSizes) recommendedSize(memory float64) string {
	s.m.Lock()
	defer s.m.Unlock()

	sizesInOrder := s.sizesInOrderByMemory()
	for _, size := range sizesInOrder {
		resources, hasSize := s.sizes[size]
		if !hasSize {
			continue
		}
		containerMemoryCapacity := resources.Memory.AsApproximateFloat64() * s.kasMemoryFraction()
		if containerMemoryCapacity >= memory {
			return size
		}
	}
	// Best effort: return the largest cluster size
	return sizesInOrder[len(sizesInOrder)-1]
}

func SetupWithManager(mgr ctrl.Manager) error {
	controller := &ControlPlaneAutoscalerController{
		Client: mgr.GetClient(),
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&hyperv1.HostedCluster{}).
		Watches(&vpaautoscalingv1.VerticalPodAutoscaler{}, handler.EnqueueRequestsFromMapFunc(hostedClusterForVPA)).
		Complete(controller); err != nil {
		return err
	}
	return nil
}

func hostedClusterForVPA(ctx context.Context, object client.Object) []reconcile.Request {
	vpa, isVPA := object.(*vpaautoscalingv1.VerticalPodAutoscaler)
	if !isVPA {
		return nil
	}
	ns := vpa.Annotations[hcNamespaceAnnotation]
	name := vpa.Annotations[hcNameAnnotation]
	if ns == "" || name == "" {
		return nil
	}
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      name,
			},
		},
	}
}

func (r *ControlPlaneAutoscalerController) updateSizeCache(ctx context.Context) error {
	config := &schedulingv1alpha1.ClusterSizingConfiguration{}
	config.Name = "cluster"
	if err := r.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		return fmt.Errorf("failed to fetch cluster sizing configuration: %w", err)
	}

	listMachineSets := func() (*machinev1beta1.MachineSetList, error) {
		result := &machinev1beta1.MachineSetList{}
		err := r.List(ctx, result, client.InNamespace("openshift-machine-api"))
		return result, err
	}

	log := ctrl.LoggerFrom(ctx)
	if err := r.sizeCache.update(config, listMachineSets, log); err != nil {
		return fmt.Errorf("failed to update machine size cache: %w", err)
	}
	return nil
}

func (r *ControlPlaneAutoscalerController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	if err := r.updateSizeCache(ctx); err != nil {
		return ctrl.Result{}, err
	}

	hc := &hyperv1.HostedCluster{}
	if err := r.Get(ctx, request.NamespacedName, hc); err != nil {
		return ctrl.Result{}, err
	}

	if !hc.DeletionTimestamp.IsZero() {
		// No need to process deleting HostedClusters
		return ctrl.Result{}, nil
	}

	cpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	vpa := controlplaneautoscalermanifests.KubeAPIServerVerticalPodAutoscaler(cpNamespace)

	if hc.Annotations[hyperv1.ResourceBasedControlPlaneAutoscalingAnnotation] != "true" || hc.Annotations[hyperv1.TopologyAnnotation] != hyperv1.DedicatedRequestServingComponentsTopology {
		_, err := util.DeleteIfNeeded(ctx, r, vpa)
		return ctrl.Result{}, err
	}

	// Ensure VPA is up to date
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, vpa, reconcileKubeAPIServerVPAFunc(vpa, hc)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile kube apiserver vpa: %w", err)
	}

	if !isVPAReady(vpa) {
		return ctrl.Result{}, nil
	}

	if err := r.updateSizeRecommendation(ctx, vpa, hc); err != nil {
		return ctrl.Result{}, err
	}
	return reconcile.Result{}, nil
}

func reconcileKubeAPIServerVPAFunc(vpa *vpaautoscalingv1.VerticalPodAutoscaler, hc *hyperv1.HostedCluster) func() error {
	return func() error {
		if vpa.Annotations == nil {
			vpa.Annotations = map[string]string{}
		}
		vpa.Annotations[hcNamespaceAnnotation] = hc.Namespace
		vpa.Annotations[hcNameAnnotation] = hc.Name

		if vpa.Spec.UpdatePolicy == nil {
			vpa.Spec.UpdatePolicy = &vpaautoscalingv1.PodUpdatePolicy{}
		}
		vpa.Spec.UpdatePolicy.UpdateMode = ptr.To(vpaautoscalingv1.UpdateModeOff)

		if vpa.Spec.TargetRef == nil {
			vpa.Spec.TargetRef = &autoscalingv1.CrossVersionObjectReference{}
		}
		vpa.Spec.TargetRef.APIVersion = appsv1.SchemeGroupVersion.String()
		vpa.Spec.TargetRef.Kind = "Deployment"
		vpa.Spec.TargetRef.Name = "kube-apiserver"

		return nil
	}
}

func (r *ControlPlaneAutoscalerController) updateSizeRecommendation(ctx context.Context, vpa *vpaautoscalingv1.VerticalPodAutoscaler, hc *hyperv1.HostedCluster) error {
	recommendedSize := recommendedClusterSize(vpa.Status.Recommendation, &r.sizeCache)
	if recommendedSize == "" {
		return fmt.Errorf("cannot determine recommended cluster size with current sizes")
	}
	if hc.Annotations[hyperv1.RecommendedClusterSizeAnnotation] == recommendedSize {
		// No update needed
		return nil
	}

	patchedHC := hc.DeepCopy()
	patchedHC.Annotations[hyperv1.RecommendedClusterSizeAnnotation] = recommendedSize
	if err := r.Patch(ctx, patchedHC, client.MergeFrom(hc)); err != nil {
		return fmt.Errorf("failed to patch HostedCluster: %w", err)
	}
	return nil
}

func isVPAReady(vpa *vpaautoscalingv1.VerticalPodAutoscaler) bool {
	recommendationProvidedCondition := findVPACondition(vpa.Status.Conditions, vpaautoscalingv1.RecommendationProvided)
	if recommendationProvidedCondition == nil || recommendationProvidedCondition.Status != corev1.ConditionTrue || vpa.Status.Recommendation == nil {
		return false
	}
	return true
}

func findVPACondition(conditions []vpaautoscalingv1.VerticalPodAutoscalerCondition, conditionType vpaautoscalingv1.VerticalPodAutoscalerConditionType) *vpaautoscalingv1.VerticalPodAutoscalerCondition {
	for i, cond := range conditions {
		for cond.Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func recommendedClusterSize(recommendation *vpaautoscalingv1.RecommendedPodResources, sizeCache *machineSizes) string {
	var recommendedMemory *resource.Quantity
	for _, containerRecommendation := range recommendation.ContainerRecommendations {
		if containerRecommendation.ContainerName == "kube-apiserver" {
			recommendedMemory = containerRecommendation.UncappedTarget.Memory()
		}
	}
	if recommendedMemory == nil {
		return "" // No recommended memory was found or set yet
	}
	return sizeCache.recommendedSize(recommendedMemory.AsApproximateFloat64())
}
