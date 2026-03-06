package resourcebasedcpautoscaler

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	controlplaneautoscalermanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneautoscaler"
	"github.com/openshift/hypershift/support/util"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName = "ResourceBasedControlPlaneAutoscaler"

	// kubeAPIServerMemorySizeFractionDefault is used to determine whether the VPA recommendation fits within a cluster size.
	// The HostedCluster will be sized to the smallest size for which capacity * kubeAPIServerMemorySizeFractionDefault >= recommendation.
	kubeAPIServerMemorySizeFractionDefault = 0.65

	// kubeAPIServerCPUSizeFractionDefault is used to determine whether the VPA CPU recommendation fits within a cluster size.
	// The HostedCluster will be sized to the smallest size for which capacity * kubeAPIServerCPUSizeFractionDefault >= recommendation.
	kubeAPIServerCPUSizeFractionDefault = 0.65

	// hosted cluster namespace annotation on VPA
	hcNamespaceAnnotation = "hypershift.openshift.io/cluster-namespace"

	// hosted cluster name annotation on VPA
	hcNameAnnotation = "hypershift.openshift.io/cluster-name"
)

type ControlPlaneAutoscalerController struct {
	client.Client
	sizeCache machineSizesCache
	// updateSizeCacheFunc is used to stub the real function for testing
	updateSizeCacheFunc func(ctx context.Context) error
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
	if r.updateSizeCacheFunc != nil {
		return r.updateSizeCacheFunc(ctx)
	}
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
		if apierrors.IsNotFound(err) {
			// HostedCluster was deleted or does not exist; nothing to do
			return ctrl.Result{}, nil
		}
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

	if ready, err := isVPAReady(vpa); !ready {
		log := ctrl.LoggerFrom(ctx)
		log.Info("VPA is not ready for recommendations", "reason", err.Error())
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
	log := ctrl.LoggerFrom(ctx)

	// Get the kube-apiserver memory and CPU recommendations for logging
	var kubeAPIServerMemory *resource.Quantity
	var kubeAPIServerCPU *resource.Quantity
	for _, containerRec := range vpa.Status.Recommendation.ContainerRecommendations {
		if containerRec.ContainerName == "kube-apiserver" {
			kubeAPIServerMemory = containerRec.UncappedTarget.Memory()
			kubeAPIServerCPU = containerRec.UncappedTarget.Cpu()
			break
		}
	}

	recommendedSize := recommendedClusterSize(vpa.Status.Recommendation, &r.sizeCache)
	if recommendedSize == "" {
		log.V(1).Info("No cluster size recommendation available",
			"reason", "no kube-apiserver recommendation found",
			"containerCount", len(vpa.Status.Recommendation.ContainerRecommendations))
		return nil
	}

	currentSize := ""
	if hc.Annotations != nil {
		currentSize = hc.Annotations[hyperv1.RecommendedClusterSizeAnnotation]
	}

	if currentSize == recommendedSize {
		logKVs := []interface{}{
			"size", recommendedSize,
		}
		if kubeAPIServerMemory != nil {
			logKVs = append(logKVs, "kubeAPIServerMemory", kubeAPIServerMemory.String())
		}
		if kubeAPIServerCPU != nil {
			logKVs = append(logKVs, "kubeAPIServerCPU", kubeAPIServerCPU.String())
		}
		log.V(2).Info("Cluster size recommendation unchanged", logKVs...)
		return nil
	}

	// Log the size change with detailed information
	logKVs := []interface{}{
		"previousSize", currentSize,
		"newSize", recommendedSize,
	}
	if kubeAPIServerMemory != nil {
		logKVs = append(logKVs, "kubeAPIServerMemory", kubeAPIServerMemory.String())
		logKVs = append(logKVs, "effectiveMemoryFraction", r.sizeCache.effectiveMemoryFraction(recommendedSize))
	}
	if kubeAPIServerCPU != nil {
		logKVs = append(logKVs, "kubeAPIServerCPU", kubeAPIServerCPU.String())
		logKVs = append(logKVs, "effectiveCPUFraction", r.sizeCache.effectiveCPUFraction(recommendedSize))
	}
	log.Info("Updating cluster size recommendation", logKVs...)

	patchedHC := hc.DeepCopy()
	if patchedHC.Annotations == nil {
		patchedHC.Annotations = make(map[string]string)
	}
	patchedHC.Annotations[hyperv1.RecommendedClusterSizeAnnotation] = recommendedSize
	if err := r.Patch(ctx, patchedHC, client.MergeFrom(hc)); err != nil {
		return fmt.Errorf("failed to patch HostedCluster: %w", err)
	}

	log.Info("Successfully updated cluster size recommendation",
		"hostedCluster", hc.Name,
		"namespace", hc.Namespace,
		"newSize", recommendedSize)
	return nil
}

// isVPAReady checks if the VPA has valid recommendations available for processing.
// Returns true if ready, false if not ready, and an error describing what's missing.
func isVPAReady(vpa *vpaautoscalingv1.VerticalPodAutoscaler) (bool, error) {
	if vpa.Status.Recommendation == nil {
		return false, fmt.Errorf("VPA recommendation is nil")
	}

	recommendationProvidedCondition := findVPACondition(vpa.Status.Conditions, vpaautoscalingv1.RecommendationProvided)
	if recommendationProvidedCondition == nil {
		return false, fmt.Errorf("VPA RecommendationProvided condition not found")
	}

	if recommendationProvidedCondition.Status != corev1.ConditionTrue {
		return false, fmt.Errorf("VPA RecommendationProvided condition is %s, message: %s",
			recommendationProvidedCondition.Status, recommendationProvidedCondition.Message)
	}

	// Additional validation: check if we have container recommendations
	if len(vpa.Status.Recommendation.ContainerRecommendations) == 0 {
		return false, fmt.Errorf("VPA recommendation contains no container recommendations")
	}

	return true, nil
}

func findVPACondition(conditions []vpaautoscalingv1.VerticalPodAutoscalerCondition, conditionType vpaautoscalingv1.VerticalPodAutoscalerConditionType) *vpaautoscalingv1.VerticalPodAutoscalerCondition {
	for i, cond := range conditions {
		if cond.Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// recommendedClusterSize determines the optimal cluster size class based on VPA recommendations
// for the kube-apiserver container, considering both memory and CPU.
//
// The function implements the following logic:
// 1. Extracts both memory and CPU recommendations for the "kube-apiserver" container from VPA
// 2. Uses the UncappedTarget values, which represent VPA's recommendations without resource limits
// 3. Delegates to the size cache to find the smallest cluster size that can accommodate both requirements
//
// Size selection algorithm:
// - If both memory and CPU recommendations are available, uses recommendedSizeByBoth which
//   independently determines the recommended size for each resource and returns the larger of the two
// - If only memory is available, falls back to memory-only sizing (backward compatible)
// - If only CPU is available, uses CPU-only sizing
// - The cache considers effective fractions (per-size > global > default) when calculating capacity
//
// Returns:
// - Empty string if no kube-apiserver recommendation is found or cache is empty
// - Size class name (e.g., "small", "medium", "large") that can accommodate the workload
func recommendedClusterSize(recommendation *vpaautoscalingv1.RecommendedPodResources, sizeCache *machineSizesCache) string {
	// Extract memory and CPU recommendations for kube-apiserver container
	var recommendedMemory *resource.Quantity
	var recommendedCPU *resource.Quantity
	for _, containerRecommendation := range recommendation.ContainerRecommendations {
		if containerRecommendation.ContainerName == "kube-apiserver" {
			recommendedMemory = containerRecommendation.UncappedTarget.Memory()
			recommendedCPU = containerRecommendation.UncappedTarget.Cpu()
			break // Found the kube-apiserver recommendation
		}
	}

	if recommendedMemory == nil && recommendedCPU == nil {
		// No kube-apiserver container found in VPA recommendations
		return ""
	}

	// Delegate to cache for size class selection
	switch {
	case recommendedMemory != nil && recommendedCPU != nil:
		return sizeCache.recommendedSizeByBoth(recommendedMemory.AsApproximateFloat64(), recommendedCPU.AsApproximateFloat64())
	case recommendedMemory != nil:
		return sizeCache.recommendedSize(recommendedMemory.AsApproximateFloat64())
	default:
		return sizeCache.recommendedSizeByCPU(recommendedCPU.AsApproximateFloat64())
	}
}
