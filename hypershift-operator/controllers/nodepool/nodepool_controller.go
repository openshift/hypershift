package nodepool

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	k8sutilspointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/machineimage"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util"
	"github.com/openshift/hypershift/thirdparty/clusterapi/util/patch"
	capiaws "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha3"
)

const (
	finalizer               = "hypershift.openshift.io/finalizer"
	autoscalerMaxAnnotation = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"
	autoscalerMinAnnotation = "cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size"
	nodePoolAnnotation      = "hypershift.openshift.io/nodePool"
)

type NodePoolReconciler struct {
	ctrlclient.Client
	recorder      record.EventRecorder
	Log           logr.Logger
	ImageProvider machineimage.Provider
}

func (r *NodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.NodePool{}).
		Watches(&source.Kind{Type: &capiv1.MachineDeployment{}}, handler.EnqueueRequestsFromMapFunc(enqueueParentNodePool)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Build(r)
	if err != nil {
		return errors.Wrap(err, "failed setting up with a controller manager")
	}

	r.recorder = mgr.GetEventRecorderFor("nodepool-controller")

	return nil
}

func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = ctrl.LoggerFrom(ctx)
	r.Log.Info("Reconciling")

	// Fetch the nodePool instance
	nodePool := &hyperv1.NodePool{}
	err := r.Client.Get(ctx, req.NamespacedName, nodePool)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.Log.Info("not found")
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "error getting nodePool")
		return ctrl.Result{}, err
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	targetNamespace := manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name).Name
	// Ignore deleted nodePools, this can happen when foregroundDeletion
	// is enabled
	if !nodePool.DeletionTimestamp.IsZero() {
		ami, err := r.ImageProvider.Image(hcluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to obtain AMI: %w", err)
		}
		machineSet, _, err := generateScalableResources(r, ctx, hcluster.Spec.InfraID, hcluster.Spec.Platform.AWS.Region, ami, nodePool, targetNamespace)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to generate worker machineset: %w", err)
		}
		if err := r.Delete(ctx, machineSet); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to delete nodePool: %w", err)
		}

		if controllerutil.ContainsFinalizer(nodePool, finalizer) {
			controllerutil.RemoveFinalizer(nodePool, finalizer)
			if err := r.Update(ctx, nodePool); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from nodePool: %w", err)
			}
		}
		r.Log.Info("Deleted machineSet", "machineset", machineSet.GetName())
		return ctrl.Result{}, nil
	}

	// Ensure the nodePool has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(nodePool, finalizer) {
		controllerutil.AddFinalizer(nodePool, finalizer)
		if err := r.Update(ctx, nodePool); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to nodePool: %w", err)
		}
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(nodePool, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	result, err := r.reconcile(ctx, hcluster, nodePool)
	if err != nil {
		r.Log.Error(err, "Failed to reconcile nodePool")
		r.recorder.Eventf(nodePool, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		if err := patchHelper.Patch(ctx, nodePool); err != nil {
			r.Log.Error(err, "failed to patch")
			return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
		}
		return result, err
	}

	if err := patchHelper.Patch(ctx, nodePool); err != nil {
		r.Log.Error(err, "failed to patch")
		return ctrl.Result{}, fmt.Errorf("failed to patch: %w", err)
	}

	r.Log.Info("Successfully reconciled")
	return result, nil
}

func (r *NodePoolReconciler) reconcile(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconcile nodePool")

	nodePool.OwnerReferences = util.EnsureOwnerRef(nodePool.OwnerReferences, metav1.OwnerReference{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedCluster",
		Name:       hcluster.Name,
		UID:        hcluster.UID,
	})

	// Validate input
	if err := validate(nodePool); err != nil {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:    hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.NodePoolValidationFailedConditionReason,
			Message: err.Error(),
		})
		return reconcile.Result{}, fmt.Errorf("error validating autoscaling parameters: %w", err)
	}

	// Generate scalable resource for nodePool
	targetNamespace := manifests.HostedControlPlaneNamespaceName(hcluster.Namespace, hcluster.Name).Name
	ami, err := r.ImageProvider.Image(hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to obtain AMI: %w", err)
	}
	scalableResource, AWSMachineTemplate, err := generateScalableResources(r, ctx,
		hcluster.Spec.InfraID,
		hcluster.Spec.Platform.AWS.Region,
		ami,
		nodePool,
		targetNamespace)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to generate worker machineset: %w", err)
	}

	// Get wanted replicas
	wantedReplicas := Int32PtrDerefOr(nodePool.Spec.NodeCount, 0)
	isAutoscalingEnabled := isAutoscalingEnabled(nodePool)
	if isAutoscalingEnabled {
		currentMachineDeployment := &capiv1.MachineDeployment{}
		if err := r.Client.Get(ctx, ctrlclient.ObjectKey{
			Name:      scalableResource.GetName(),
			Namespace: scalableResource.GetNamespace()},
			currentMachineDeployment); err != nil {
			if !apierrors.IsNotFound(err) {
				return reconcile.Result{}, err
			}
			// if autoscaling is enabled and the machineSet does not exist yet
			// start with 1 replica as the autoscaler does not support scaling from zero yet.
			wantedReplicas = int32(1)
		}
	}

	// Persist scalable resource and provider template
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, AWSMachineTemplate, func() error { return nil }); err != nil {
		return ctrl.Result{}, err
	}
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, scalableResource, func() error {
		if !isAutoscalingEnabled {
			scalableResource.Spec.Replicas = &wantedReplicas
			scalableResource.Annotations[autoscalerMaxAnnotation] = "0"
			scalableResource.Annotations[autoscalerMinAnnotation] = "0"

		}
		// If autoscaling is enabled we don't modify the scalable resource replicas
		if isAutoscalingEnabled {
			scalableResource.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Max)
			scalableResource.Annotations[autoscalerMinAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Min)
		}
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	// Update Status.nodeCount and conditions
	nodePool.Status.NodeCount = int(scalableResource.Status.AvailableReplicas)
	if !isAutoscalingEnabled {
		meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
			Type:   hyperv1.NodePoolAutoscalingEnabledConditionType,
			Status: metav1.ConditionFalse,
			Reason: hyperv1.NodePoolAsExpectedConditionReason,
		})

		if nodePool.Status.NodeCount != int(*nodePool.Spec.NodeCount) {
			log.Info("Requeueing nodePool", "expected available nodes", *nodePool.Spec.NodeCount, "current available nodes", nodePool.Status.NodeCount)
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}

	log.Info("NodePool autoscaling is enabled",
		"Maximum nodes", *nodePool.Spec.AutoScaling.Max,
		"Minimum nodes", *nodePool.Spec.AutoScaling.Min)

	meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
		Type:    hyperv1.NodePoolAutoscalingEnabledConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.NodePoolAsExpectedConditionReason,
		Message: fmt.Sprintf("Maximum nodes: %v, Minimum nodes: %v", *nodePool.Spec.AutoScaling.Max, *nodePool.Spec.AutoScaling.Min),
	})

	return ctrl.Result{}, nil
}

// GetHostedClusterByName finds and return a HostedCluster object using the specified params.
func GetHostedClusterByName(ctx context.Context, c client.Client, namespace, name string) (*hyperv1.HostedCluster, error) {
	hcluster := &hyperv1.HostedCluster{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	if err := c.Get(ctx, key, hcluster); err != nil {
		return nil, err
	}

	return hcluster, nil
}

func generateScalableResources(client ctrlclient.Client, ctx context.Context,
	infraName, region, ami string, nodePool *hyperv1.NodePool, targetNamespace string) (*capiv1.MachineDeployment, *capiaws.AWSMachineTemplate, error) {
	subnet := &capiaws.AWSResourceReference{}
	if nodePool.Spec.Platform.AWS.Subnet != nil {
		subnet.ID = nodePool.Spec.Platform.AWS.Subnet.ID
		subnet.ARN = nodePool.Spec.Platform.AWS.Subnet.ARN
		for k := range nodePool.Spec.Platform.AWS.Subnet.Filters {
			filter := capiaws.Filter{
				Name:   nodePool.Spec.Platform.AWS.Subnet.Filters[k].Name,
				Values: nodePool.Spec.Platform.AWS.Subnet.Filters[k].Values,
			}
			subnet.Filters = append(subnet.Filters, filter)
		}
	}
	securityGroups := []capiaws.AWSResourceReference{}
	for _, sg := range nodePool.Spec.Platform.AWS.SecurityGroups {
		filters := []capiaws.Filter{}
		for _, f := range sg.Filters {
			filters = append(filters, capiaws.Filter{
				Name:   f.Name,
				Values: f.Values,
			})
		}
		securityGroups = append(securityGroups, capiaws.AWSResourceReference{
			ARN:     sg.ARN,
			ID:      sg.ID,
			Filters: filters,
		})
	}

	instanceProfile := fmt.Sprintf("%s-worker-profile", infraName)
	if nodePool.Spec.Platform.AWS.InstanceProfile != "" {
		instanceProfile = nodePool.Spec.Platform.AWS.InstanceProfile
	}

	instanceType := nodePool.Spec.Platform.AWS.InstanceType
	resourcesName := generateName(infraName, nodePool.Spec.ClusterName, nodePool.GetName())
	dataSecretName := fmt.Sprintf("%s-user-data", nodePool.Spec.ClusterName)

	AWSMachineTemplate := &capiaws.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourcesName,
			Namespace: targetNamespace,
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					UncompressedUserData: k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					IAMInstanceProfile: instanceProfile,
					InstanceType:       instanceType,
					AMI: capiaws.AWSResourceReference{
						ID: k8sutilspointer.StringPtr(ami),
					},
					AdditionalSecurityGroups: securityGroups,
					Subnet:                   subnet,
				},
			},
		},
	}

	annotations := map[string]string{
		nodePoolAnnotation: ctrlclient.ObjectKeyFromObject(nodePool).String(),
	}
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)
	if isAutoscalingEnabled(nodePool) {
		if nodePool.Spec.AutoScaling.Max != nil &&
			*nodePool.Spec.AutoScaling.Max > 0 {
			annotations[autoscalerMinAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Min)
			annotations[autoscalerMaxAnnotation] = strconv.Itoa(*nodePool.Spec.AutoScaling.Max)
		}
	}
	machineDeployment := &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourcesName,
			Namespace:   targetNamespace,
			Annotations: annotations,
			Labels: map[string]string{
				capiv1.ClusterLabelName: infraName,
			},
		},
		TypeMeta: metav1.TypeMeta{},
		Spec: capiv1.MachineDeploymentSpec{
			Strategy: &capiv1.MachineDeploymentStrategy{
				Type: capiv1.RollingUpdateMachineDeploymentStrategyType,
				RollingUpdate: &capiv1.MachineRollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
			ClusterName: infraName,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					resourcesName: resourcesName,
				},
			},
			Template: capiv1.MachineTemplateSpec{
				ObjectMeta: capiv1.ObjectMeta{
					Labels: map[string]string{
						resourcesName:           resourcesName,
						capiv1.ClusterLabelName: infraName,
					},
					// TODO (alberto): drop/expose this annotation at the nodePool API
					Annotations: map[string]string{
						"machine.cluster.x-k8s.io/exclude-node-draining": "true",
					},
				},
				Spec: capiv1.MachineSpec{
					Bootstrap: capiv1.Bootstrap{
						DataSecretName: &dataSecretName,
					},
					ClusterName: nodePool.Spec.ClusterName,
					InfrastructureRef: corev1.ObjectReference{
						Namespace:  nodePool.GetNamespace(),
						Name:       resourcesName,
						APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha3",
						Kind:       "AWSMachineTemplate",
					},
				},
			},
		},
	}

	return machineDeployment, AWSMachineTemplate, nil
}

func generateName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 43)
}

// getName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
func getName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	// length of -hash-
	baseLength := maxLength - 10 - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 0 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, hash(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hash(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}

func isAutoscalingEnabled(nodePool *hyperv1.NodePool) bool {
	return nodePool.Spec.AutoScaling != nil
}

func validate(nodePool *hyperv1.NodePool) error {
	if nodePool.Spec.NodeCount != nil && nodePool.Spec.AutoScaling != nil {
		return fmt.Errorf("only one of nodePool.Spec.NodeCount or nodePool.Spec.AutoScaling can be set")
	}

	if nodePool.Spec.AutoScaling != nil {
		max := nodePool.Spec.AutoScaling.Max
		min := nodePool.Spec.AutoScaling.Min

		if max == nil || min == nil {
			return fmt.Errorf("max and min must be not nil. Max: %v, Min: %v", max, min)
		}

		if *max < *min {
			return fmt.Errorf("max must be equal or greater than min. Max: %v, Min: %v", *max, *min)
		}

		if *max == 0 && *min == 0 {
			return fmt.Errorf("max and min must be not zero. Max: %v, Min: %v", *max, *min)
		}
	}

	return nil
}

// Int32PtrDerefOr dereference the int32 ptr and returns it if not nil,
// else returns def.
func Int32PtrDerefOr(ptr *int32, def int32) int32 {
	if ptr != nil {
		return *ptr
	}
	return def
}

func enqueueParentNodePool(obj ctrlclient.Object) []reconcile.Request {
	var nodePoolName string
	if obj.GetAnnotations() != nil {
		nodePoolName = obj.GetAnnotations()[nodePoolAnnotation]
	}
	if nodePoolName == "" {
		return []reconcile.Request{}
	}
	return []reconcile.Request{
		{NamespacedName: hyperutil.ParseNamespacedName(nodePoolName)},
	}
}
