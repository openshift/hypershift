package machinedeployment

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/machinedeployment/utils"
	"github.com/openshift/hypershift/support/awsclient"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// These expose compute information based on the providerSpec input.
	// This is needed by the autoscaler to foresee upcoming capacity when scaling from zero.
	// https://github.com/openshift/enhancements/pull/186
	cpuKey       = "machine.openshift.io/vCPU"
	memoryKey    = "machine.openshift.io/memoryMb"
	gpuKey       = "machine.openshift.io/GPU"
	labelsKey    = "capacity.cluster-autoscaler.kubernetes.io/labels"
	taintsKey    = "capacity.cluster-autoscaler.kubernetes.io/taints"
	archLabelKey = "kubernetes.io/arch"

	controlPlaneNamespaceLabel = "hypershift.openshift.io/hosted-control-plane"
	nodePoolAnnotation         = "hypershift.openshift.io/nodePool"
)

// Reconciler reconciles MachineDeployments for scale-from-zero autoscaling.
type Reconciler struct {
	Client             client.Client
	CredentialsFile    string
	RegionCache        awsclient.RegionCache
	InstanceTypesCache InstanceTypesCache

	recorder record.EventRecorder
	scheme   *runtime.Scheme
}

// SetupWithManager creates a new controller for a manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1.MachineDeployment{}, builder.WithPredicates(predicate.NewPredicateFuncs(r.hasNodePoolAnnotation))).
		Watches(
			&capiaws.AWSMachineTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.machineDeploymentsForTemplate),
			builder.WithPredicates(r.statusCapacityChanged()),
		).
		Watches(
			&hyperv1.NodePool{},
			handler.EnqueueRequestsFromMapFunc(r.machineDeploymentsForNodePool),
			builder.WithPredicates(r.nodePoolLabelsOrTaintsChanged()),
		).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	r.recorder = mgr.GetEventRecorderFor("machinedeployment-controller")
	r.scheme = mgr.GetScheme()
	return nil
}

// hasNodePoolAnnotation filters MachineDeployments to only those belonging to HyperShift NodePools.
func (r *Reconciler) hasNodePoolAnnotation(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, exists := annotations[nodePoolAnnotation]
	return exists
}

// statusCapacityChanged returns a predicate that triggers on Status.Capacity changes.
func (r *Reconciler) statusCapacityChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldTemplate, ok := e.ObjectOld.(*capiaws.AWSMachineTemplate)
			if !ok {
				return false
			}
			newTemplate, ok := e.ObjectNew.(*capiaws.AWSMachineTemplate)
			if !ok {
				return false
			}
			// Trigger if Status.Capacity changed from empty to populated
			return len(oldTemplate.Status.Capacity) == 0 && len(newTemplate.Status.Capacity) > 0
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// machineDeploymentsForTemplate finds MachineDeployments referencing the template.
func (r *Reconciler) machineDeploymentsForTemplate(ctx context.Context, obj client.Object) []reconcile.Request {
	template, ok := obj.(*capiaws.AWSMachineTemplate)
	if !ok {
		return nil
	}

	mdList := &clusterv1.MachineDeploymentList{}
	if err := r.Client.List(ctx, mdList, client.InNamespace(template.Namespace)); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, md := range mdList.Items {
		if md.Spec.Template.Spec.InfrastructureRef.Name == template.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: md.Namespace,
					Name:      md.Name,
				},
			})
		}
	}
	return requests
}

// nodePoolLabelsOrTaintsChanged returns a predicate that triggers on NodePool label or taint changes.
func (r *Reconciler) nodePoolLabelsOrTaintsChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNodePool, ok := e.ObjectOld.(*hyperv1.NodePool)
			if !ok {
				return false
			}
			newNodePool, ok := e.ObjectNew.(*hyperv1.NodePool)
			if !ok {
				return false
			}
			// Trigger if NodeLabels or Taints changed
			labelsChanged := !reflect.DeepEqual(oldNodePool.Spec.NodeLabels, newNodePool.Spec.NodeLabels)
			taintsChanged := !reflect.DeepEqual(oldNodePool.Spec.Taints, newNodePool.Spec.Taints)
			return labelsChanged || taintsChanged
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// machineDeploymentsForNodePool finds MachineDeployments owned by the NodePool.
func (r *Reconciler) machineDeploymentsForNodePool(ctx context.Context, obj client.Object) []reconcile.Request {
	nodePool, ok := obj.(*hyperv1.NodePool)
	if !ok {
		return nil
	}

	// The NodePool annotation format on MachineDeployments is "namespace/name"
	expectedAnnotation := fmt.Sprintf("%s/%s", nodePool.Namespace, nodePool.Name)

	// Find all MachineDeployments in the control plane namespace
	// The control plane namespace is derived from the HostedCluster
	// For now, we'll search all namespaces and filter by annotation
	mdList := &clusterv1.MachineDeploymentList{}
	if err := r.Client.List(ctx, mdList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, md := range mdList.Items {
		if mdAnnotation, ok := md.Annotations[nodePoolAnnotation]; ok && mdAnnotation == expectedAnnotation {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: md.Namespace,
					Name:      md.Name,
				},
			})
		}
	}
	return requests
}

// resolveNodePool fetches the NodePool referenced by the MachineDeployment's annotation.
func (r *Reconciler) resolveNodePool(ctx context.Context, machineDeployment *clusterv1.MachineDeployment) (*hyperv1.NodePool, error) {
	nodePoolAnnotationValue, ok := machineDeployment.Annotations[nodePoolAnnotation]
	if !ok || nodePoolAnnotationValue == "" {
		return nil, fmt.Errorf("MachineDeployment is missing %s annotation", nodePoolAnnotation)
	}

	// The annotation format is "namespace/name"
	parts := strings.SplitN(nodePoolAnnotationValue, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid NodePool annotation format: expected 'namespace/name', got %q", nodePoolAnnotationValue)
	}

	nodePool := &hyperv1.NodePool{}
	nodePoolKey := types.NamespacedName{
		Namespace: parts[0],
		Name:      parts[1],
	}

	if err := r.Client.Get(ctx, nodePoolKey, nodePool); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("NodePool %s not found: %w", nodePoolKey.String(), err)
		}
		return nil, fmt.Errorf("failed to get NodePool %s: %w", nodePoolKey.String(), err)
	}

	return nodePool, nil
}

// taintsToAnnotation converts HyperShift taints to the CAPI format.
// Format: "key=value:effect,key2=value2:effect2"
// See: https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210310-opt-in-autoscaling-from-zero.md
func taintsToAnnotation(taints []hyperv1.Taint) string {
	if len(taints) == 0 {
		return ""
	}

	var parts []string
	for _, t := range taints {
		parts = append(parts, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
	}
	// Sort for deterministic output
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// Reconcile implements controller runtime Reconciler interface.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(3).Info("Reconciling")

	machineDeployment := &clusterv1.MachineDeployment{}
	if err := r.Client.Get(ctx, req.NamespacedName, machineDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Early check: skip if not an AWS MachineDeployment (before namespace fetch)
	infraRef := machineDeployment.Spec.Template.Spec.InfrastructureRef
	if infraRef.Kind != "AWSMachineTemplate" || infraRef.Name == "" {
		log.V(4).Info("Skipping non-AWS MachineDeployment", "kind", infraRef.Kind, "name", infraRef.Name)
		return ctrl.Result{}, nil
	}

	// Skip if namespace is not a control-plane namespace
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: machineDeployment.Namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get namespace: %w", err)
	}
	if ns.Labels == nil || ns.Labels[controlPlaneNamespaceLabel] != "true" {
		log.V(4).Info("Skipping non-control-plane namespace", "namespace", machineDeployment.Namespace)
		return ctrl.Result{}, nil
	}

	// Ignore deleted MachineDeployments, this can happen when foregroundDeletion
	// is enabled
	if !machineDeployment.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	originalMachineDeployment := machineDeployment.DeepCopy()

	result, err := r.reconcile(ctx, machineDeployment)
	if err != nil {
		log.Error(err, "Failed to reconcile MachineDeployment")
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		// we don't return here so we want to attempt to patch the machine regardless of an error.
	}

	// Only patch if annotations changed
	if !reflect.DeepEqual(originalMachineDeployment.Annotations, machineDeployment.Annotations) {
		if patchErr := r.Client.Patch(ctx, machineDeployment, client.MergeFrom(originalMachineDeployment)); patchErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch machineDeployment: %w", patchErr)
		}
	}

	return result, err
}

func (r *Reconciler) reconcile(ctx context.Context, machineDeployment *clusterv1.MachineDeployment) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(3).Info("Reconciling MachineDeployment", "machineDeployment", machineDeployment.Name)

	// Resolve AWSMachineTemplate first
	awsMachineTemplate, err := utils.ResolveAWSMachineTemplate(ctx, r.Client, machineDeployment)
	if err != nil {
		// Retryable error - could be transient API failure
		return ctrl.Result{}, err
	}

	// Check if AWSMachineTemplate.Status.Capacity is already populated by CAPA
	if len(awsMachineTemplate.Status.Capacity) > 0 {
		log.V(3).Info("AWSMachineTemplate has Status.Capacity set by CAPA",
			"namespace", awsMachineTemplate.Namespace, "name", awsMachineTemplate.Name)

		// Clean up workaround annotations if they exist
		changed := false
		for _, key := range []string{cpuKey, memoryKey, gpuKey, labelsKey, taintsKey} {
			if _, exists := machineDeployment.Annotations[key]; exists {
				delete(machineDeployment.Annotations, key)
				changed = true
			}
		}

		if changed {
			log.Info("Removing workaround annotations since CAPA provides Status.Capacity",
				"machineDeployment", machineDeployment.Name)
			// Continue to patch the MachineDeployment with removed annotations
		}

		return ctrl.Result{}, nil
	}

	// CAPA doesn't support scale-from-zero yet, use workaround
	log.V(3).Info("AWSMachineTemplate has no Status.Capacity, applying annotation workaround",
		"namespace", awsMachineTemplate.Namespace, "name", awsMachineTemplate.Name)

	// Extract instance type
	instanceType, err := utils.ExtractInstanceType(awsMachineTemplate)
	if err != nil {
		log.Error(err, "Failed to extract instance type")
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "FailedUpdate", "Failed to extract instance type: %v", err)
		return ctrl.Result{}, err
	}

	// Resolve AWS region
	region, err := utils.ResolveRegion(ctx, r.Client, machineDeployment)
	if err != nil {
		log.Error(err, "Failed to resolve AWS region")
		r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "FailedUpdate", "Failed to resolve AWS region: %v", err)
		return ctrl.Result{}, err
	}

	// Create AWS client with region validation
	awsClient, err := awsclient.NewValidatedClient(ctx, region, r.CredentialsFile, r.RegionCache)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error creating aws client: %w", err)
	}

	// Get instance type information
	instanceTypeInfo, err := r.InstanceTypesCache.GetInstanceType(ctx, awsClient, region, instanceType)
	if err != nil {
		// Permanent error: instance type doesn't exist in this region
		if strings.Contains(err.Error(), "not found") {
			r.recorder.Eventf(machineDeployment, corev1.EventTypeWarning, "UnknownInstanceType",
				"Instance type %s not found in region %s", instanceType, region)
			return ctrl.Result{}, nil // Don't requeue
		}

		// Transient error: requeue with backoff
		return ctrl.Result{}, err
	}

	// Fetch the NodePool to get labels and taints (optional - may not exist in all cases)
	nodePool, err := r.resolveNodePool(ctx, machineDeployment)
	if err != nil {
		log.V(4).Info("Could not resolve NodePool, skipping taints and node labels", "error", err.Error())
		// NodePool may not exist yet or annotation may be malformed
		// Continue without taints and node labels
		nodePool = nil
	}

	// Set annotations
	if machineDeployment.Annotations == nil {
		machineDeployment.Annotations = make(map[string]string)
	}

	machineDeployment.Annotations[cpuKey] = strconv.FormatInt(int64(instanceTypeInfo.VCPU), 10)
	machineDeployment.Annotations[memoryKey] = strconv.FormatInt(instanceTypeInfo.MemoryMb, 10)
	machineDeployment.Annotations[gpuKey] = strconv.FormatInt(int64(instanceTypeInfo.GPU), 10)

	// Parse existing labels, update architecture, and preserve user-provided labels
	labelsMap := make(map[string]string)
	if existingLabels, ok := machineDeployment.Annotations[labelsKey]; ok && existingLabels != "" {
		// Parse comma-separated labels into map
		for _, label := range strings.Split(existingLabels, ",") {
			parts := strings.SplitN(strings.TrimSpace(label), "=", 2)
			if len(parts) == 2 {
				labelsMap[parts[0]] = parts[1]
			}
		}
	}

	// Add architecture label
	labelsMap[archLabelKey] = string(instanceTypeInfo.CPUArchitecture)

	// Add NodePool.Spec.NodeLabels to the labels map if NodePool exists
	if nodePool != nil {
		for k, v := range nodePool.Spec.NodeLabels {
			labelsMap[k] = v
		}
	}

	// Serialize back to comma-separated format
	labels := make([]string, 0, len(labelsMap))
	for k, v := range labelsMap {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}
	// Sort for deterministic output in tests
	sort.Strings(labels)
	machineDeployment.Annotations[labelsKey] = strings.Join(labels, ",")

	// Set taints annotation from NodePool.Spec.Taints if NodePool exists
	if nodePool != nil {
		taintsAnnotation := taintsToAnnotation(nodePool.Spec.Taints)
		if taintsAnnotation != "" {
			machineDeployment.Annotations[taintsKey] = taintsAnnotation
		} else {
			// Remove taints annotation if there are no taints
			delete(machineDeployment.Annotations, taintsKey)
		}
	} else {
		// Remove taints annotation if NodePool doesn't exist
		delete(machineDeployment.Annotations, taintsKey)
	}

	return ctrl.Result{}, nil
}
