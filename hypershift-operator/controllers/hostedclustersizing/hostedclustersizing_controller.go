package hostedclustersizing

import (
	"context"
	"fmt"
	"sort"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	hccoReportsNodeCountLabel = "io.openshift.hypershift.hosted-cluster-config-operator-reports-node-count"
)

func newReconciler(
	hypershiftClient hypershiftclient.Interface,
	lister client.Client,
	now func() time.Time,
	hypershiftOperatorImage string,
	releaseProvider *releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator,
	imageMetadataProvider *hyperutil.RegistryClientImageMetadataProvider,
) *reconciler {
	return &reconciler{
		client: hypershiftClient,
		now:    now,

		getClusterSizingConfiguration: func(ctx context.Context) (*schedulingv1alpha1.ClusterSizingConfiguration, error) {
			config := schedulingv1alpha1.ClusterSizingConfiguration{}
			if err := lister.Get(ctx, types.NamespacedName{Name: "cluster"}, &config); err != nil {
				return nil, fmt.Errorf("could not get cluster sizing configuration: %w", err)
			}
			return &config, nil
		},
		getHostedCluster: func(ctx context.Context, name types.NamespacedName) (*hypershiftv1beta1.HostedCluster, error) {
			hostedCluster := hypershiftv1beta1.HostedCluster{}
			if err := lister.Get(ctx, name, &hostedCluster); err != nil {
				return nil, fmt.Errorf("could not get hosted cluster %s: %w", name.String(), err)
			}
			return &hostedCluster, nil
		},
		listHostedClusters: func(ctx context.Context) (*hypershiftv1beta1.HostedClusterList, error) {
			hostedClusters := hypershiftv1beta1.HostedClusterList{}
			if err := lister.List(ctx, &hostedClusters); err != nil {
				return nil, fmt.Errorf("failed to list hosted clusters when refreshing timeline: %w", err)
			}
			return &hostedClusters, nil
		},
		hccoReportsNodeCount: func(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster) (bool, error) {
			var pullSecret corev1.Secret
			if err := lister.Get(ctx, types.NamespacedName{Namespace: hostedCluster.Namespace, Name: hostedCluster.Spec.PullSecret.Name}, &pullSecret); err != nil {
				return false, fmt.Errorf("failed to get pull secret: %w", err)
			}
			pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
			if !ok {
				return false, fmt.Errorf("expected %s key in pull secret", corev1.DockerConfigJsonKey)
			}
			controlPlaneOperatorImage, err := hyperutil.GetControlPlaneOperatorImage(ctx, hostedCluster, releaseProvider, hypershiftOperatorImage, pullSecretBytes)
			if err != nil {
				return false, fmt.Errorf("failed to get controlPlaneOperatorImage: %w", err)
			}
			controlPlaneOperatorImageLabels, err := hyperutil.GetControlPlaneOperatorImageLabels(ctx, hostedCluster, controlPlaneOperatorImage, pullSecretBytes, imageMetadataProvider)
			if err != nil {
				return false, fmt.Errorf("failed to get controlPlaneOperatorImageLabels: %w", err)
			}

			_, hccoReportsNodeCount := controlPlaneOperatorImageLabels[hccoReportsNodeCountLabel]
			return hccoReportsNodeCount, nil
		},
		nodePoolsForHostedCluster: func(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error) {
			nodePools := hypershiftv1beta1.NodePoolList{}
			if err := lister.List(ctx, &nodePools, client.MatchingFields{hostedClusterForNodePoolIndex: client.ObjectKeyFromObject(hostedCluster).String()}); err != nil {
				return nil, fmt.Errorf("failed to list node pools for hosted cluster: %w", err)
			}
			return &nodePools, nil
		},
		hostedControlPlaneForHostedCluster: func(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error) {
			hostedControlPlane := hypershiftv1beta1.HostedControlPlane{}
			if err := lister.Get(ctx, types.NamespacedName{
				Namespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
				Name:      hostedCluster.Name,
			}, &hostedControlPlane); err != nil {
				return nil, fmt.Errorf("could not find hosted control plane for hosted cluster %s: %w", client.ObjectKeyFromObject(hostedCluster).String(), err)
			}
			return &hostedControlPlane, nil
		},
	}
}

type reconciler struct {
	client hypershiftclient.Interface

	now func() time.Time

	getClusterSizingConfiguration      func(context.Context) (*schedulingv1alpha1.ClusterSizingConfiguration, error)
	getHostedCluster                   func(context.Context, types.NamespacedName) (*hypershiftv1beta1.HostedCluster, error)
	listHostedClusters                 func(context.Context) (*hypershiftv1beta1.HostedClusterList, error)
	hccoReportsNodeCount               func(context.Context, *hypershiftv1beta1.HostedCluster) (bool, error)
	nodePoolsForHostedCluster          func(context.Context, *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.NodePoolList, error)
	hostedControlPlaneForHostedCluster func(context.Context, *hypershiftv1beta1.HostedCluster) (*hypershiftv1beta1.HostedControlPlane, error)
}

func (r *reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("Reconciling")

	config, err := r.getClusterSizingConfiguration(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}

	hostedCluster, err := r.getHostedCluster(ctx, request.NamespacedName)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	action, err := r.reconcile(ctx, request, config, hostedCluster)
	if err != nil {
		return reconcile.Result{}, err
	}
	if action != nil {
		if action.applyCfg != nil {
			if action.applyCfg.Status != nil {
				if _, err := r.client.HypershiftV1beta1().HostedClusters(request.Namespace).ApplyStatus(ctx, action.applyCfg, metav1.ApplyOptions{FieldManager: ControllerName}); err != nil {
					return reconcile.Result{}, err
				}
			} else {
				if _, err := r.client.HypershiftV1beta1().HostedClusters(request.Namespace).Apply(ctx, action.applyCfg, metav1.ApplyOptions{FieldManager: ControllerName}); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		return reconcile.Result{RequeueAfter: action.requeueAfter}, nil
	}

	return reconcile.Result{}, nil
}

type ignoreError error

type action struct {
	requeueAfter time.Duration
	applyCfg     *hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration
}

func (r *reconciler) reconcile(
	ctx context.Context, request reconcile.Request,
	config *schedulingv1alpha1.ClusterSizingConfiguration, hostedCluster *hypershiftv1beta1.HostedCluster,
) (*action, error) {
	var configValid bool
	for _, condition := range config.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ClusterSizingConfigurationValidType && condition.Status == metav1.ConditionTrue {
			configValid = true
			break
		}
	}
	if !configValid {
		// we can't put clusters into t-shirt sizes unless we have a valid configuration; we'll re-trigger when
		// the configuration object changes and can process clusters then
		return nil, nil
	}

	if !hostedCluster.ObjectMeta.DeletionTimestamp.IsZero() {
		return nil, nil
	}

	logger := ctrl.LoggerFrom(ctx)
	isPaused, duration, err := hyperutil.ProcessPausedUntilField(hostedCluster.Spec.PausedUntil, r.now())
	if err != nil {
		logger.Error(err, "error processing hosted cluster paused field")
		return nil, nil // user needs to reformat the field, returning error is useless
	}
	if isPaused {
		logger.Info("Reconciliation paused", "pausedUntil", *hostedCluster.Spec.PausedUntil)
		return &action{requeueAfter: duration}, nil
	}

	lastTransitionTime, lastSizeClass := previousTransitionFor(hostedCluster)
	currentSizeClass, sizeClassLabelPresent := hostedCluster.ObjectMeta.Labels[hypershiftv1beta1.HostedClusterSizeLabel]
	if lastTransitionTime != nil && !sizeClassLabelPresent || currentSizeClass != lastSizeClass {
		// we can't update both the status and the labels in one call, so when we have updated status but
		// have not yet updated the labels, we just need to do that first
		return &action{
			applyCfg: hypershiftv1beta1applyconfigurations.HostedCluster(hostedCluster.Name, hostedCluster.Namespace).
				WithLabels(map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: lastSizeClass}),
		}, nil
	}

	var sizeClass *schedulingv1alpha1.SizeConfiguration
	if overrideSize := hostedCluster.Annotations[hypershiftv1beta1.ClusterSizeOverrideAnnotation]; overrideSize != "" {
		// given the override size, get the size configuration
		for i, class := range config.Spec.Sizes {
			if class.Name == overrideSize {
				sizeClass = &config.Spec.Sizes[i]
			}
		}
	} else {
		nodeCount, err := r.determineNodeCount(ctx, hostedCluster, sizeClassLabelPresent)
		if err != nil {
			if _, ignore := err.(ignoreError); ignore {
				logger.Info("Ignoring error", "error", err.Error())
				return nil, nil
			}
			return nil, err
		}

		// given the node count we need to figure out if we need to transition to another t-shirt size
		for i, class := range config.Spec.Sizes {
			if class.Criteria.From <= nodeCount && (class.Criteria.To == nil || *class.Criteria.To >= nodeCount) {
				sizeClass = &config.Spec.Sizes[i]
			}
		}
	}

	if sizeClass == nil {
		logger.Error(fmt.Errorf("could not find a size class for hosted cluster"), "no size can be set on hosted cluster")
		return nil, nil
	}
	if sizeClassLabelPresent && sizeClass.Name == currentSizeClass {
		// no transition necessary, clear transient conditions
		cfg := applyCfgFor(hostedCluster,
			metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.ClusterSizeTransitionPending).
				WithStatus(metav1.ConditionFalse).
				WithReason("ClusterSizeTransitioned").
				WithMessage("The HostedCluster has transitioned to a new t-shirt size.").
				WithLastTransitionTime(metav1.NewTime(*lastTransitionTime)),
			metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.ClusterSizeTransitionRequired).
				WithStatus(metav1.ConditionFalse).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage("The HostedCluster has transitioned to a new t-shirt size.").
				WithLastTransitionTime(metav1.NewTime(*lastTransitionTime)),
		)
		if cfg != nil {
			return &action{applyCfg: cfg}, nil
		}
		return nil, nil
	}

	previousMinimumSize := uint32(0)
	if sizeClassLabelPresent {
		for _, class := range config.Spec.Sizes {
			if class.Name == currentSizeClass {
				previousMinimumSize = class.Criteria.From
			}
		}
	}
	increasingSize := previousMinimumSize < sizeClass.Criteria.From

	// third, we need to know if we're ready to transition the cluster:
	// - the hosted cluster has limits to how quickly it can transition up and down, and
	// - the management plane has limits to how many clusters can be transitioning at any time
	delayStart := time.Time{}
	if lastTransitionTime != nil {
		// if we transitioned in the past, we need to enforce the delay from there
		delayStart = *lastTransitionTime
	}
	lastComputedTime, lastComputedSizeClass := previousComputedSizeFor(hostedCluster)
	if lastComputedTime != nil && lastComputedSizeClass == sizeClass.Name {
		// we computed that the cluster should transition already; enforce the delay from that point
		delayStart = *lastComputedTime
	}
	var delay time.Duration
	var transition string
	if increasingSize {
		transition = "increase"
		delay = config.Spec.TransitionDelay.Increase.Duration
	} else {
		transition = "decrease"
		delay = config.Spec.TransitionDelay.Decrease.Duration
	}
	if r.now().Sub(delayStart) < delay {
		cfg := applyCfgFor(hostedCluster,
			metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.ClusterSizeTransitionPending).
				WithStatus(metav1.ConditionTrue).
				WithReason("TransitionDelayNotElapsed").
				WithMessage(fmt.Sprintf("HostedClusters must wait at least %s to %s in size after the cluster size changes.", delay.String(), transition)).
				WithLastTransitionTime(metav1.NewTime(r.now())),
			metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.ClusterSizeTransitionRequired).
				WithStatus(metav1.ConditionTrue).
				WithReason(sizeClass.Name).
				WithMessage("The HostedCluster will transition to a new t-shirt size.").
				WithLastTransitionTime(metav1.NewTime(r.now())),
		)
		if cfg != nil {
			return &action{applyCfg: cfg, requeueAfter: delayStart.Add(delay).Sub(r.now())}, nil
		} else {
			return nil, nil
		}
	}

	// For new clusters being added to the fleet, we have an SLA on creation time and can't afford to delay
	// the first transition, as it is required for the control plane to schedule. For other clusters, though,
	// we want to limit the amount of churn happening in order to promote the stability of the management plane.
	if scheduled := hostedCluster.Annotations[hypershiftv1beta1.HostedClusterScheduledAnnotation]; scheduled == "true" {
		hostedClusters, err := r.listHostedClusters(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list hosted clusters when calculating concurrency: %w", err)
		}

		if changes, durationUntilChanges := transitionsWithinSlidingWindow(hostedClusters, config.Spec.Concurrency.SlidingWindow.Duration, r.now()); int32(changes) >= config.Spec.Concurrency.Limit {
			cfg := applyCfgFor(hostedCluster,
				metav1applyconfigurations.Condition().
					WithType(hypershiftv1beta1.ClusterSizeTransitionPending).
					WithStatus(metav1.ConditionTrue).
					WithReason("ConcurrencyLimitReached").
					WithMessage(fmt.Sprintf("%d HostedClusters have already transitioned sizes in the last %s, more time must elapse before the next transition.", changes, config.Spec.Concurrency.SlidingWindow.Duration.String())).
					WithLastTransitionTime(metav1.NewTime(r.now())),
				metav1applyconfigurations.Condition().
					WithType(hypershiftv1beta1.ClusterSizeTransitionRequired).
					WithStatus(metav1.ConditionTrue).
					WithReason(sizeClass.Name).
					WithMessage("The HostedCluster will transition to a new t-shirt size.").
					WithLastTransitionTime(metav1.NewTime(r.now())),
			)
			if cfg != nil {
				return &action{applyCfg: cfg, requeueAfter: durationUntilChanges}, nil
			} else {
				return nil, nil
			}
		}
	}

	cfg := applyCfgFor(hostedCluster,
		metav1applyconfigurations.Condition().
			WithType(hypershiftv1beta1.ClusterSizeComputed).
			WithStatus(metav1.ConditionTrue).
			WithReason(sizeClass.Name).
			WithMessage("The HostedCluster has transitioned to a new t-shirt size.").
			WithLastTransitionTime(metav1.NewTime(r.now())),
		metav1applyconfigurations.Condition().
			WithType(hypershiftv1beta1.ClusterSizeTransitionPending).
			WithStatus(metav1.ConditionFalse).
			WithReason("ClusterSizeTransitioned").
			WithMessage("The HostedCluster has transitioned to a new t-shirt size.").
			WithLastTransitionTime(metav1.NewTime(r.now())),
		metav1applyconfigurations.Condition().
			WithType(hypershiftv1beta1.ClusterSizeTransitionRequired).
			WithStatus(metav1.ConditionFalse).
			WithReason(hypershiftv1beta1.AsExpectedReason).
			WithMessage("The HostedCluster has transitioned to a new t-shirt size.").
			WithLastTransitionTime(metav1.NewTime(r.now())),
	)
	if cfg != nil {
		return &action{applyCfg: cfg}, nil
	}
	return nil, nil
}

func (r *reconciler) determineNodeCount(ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, sizeClassLabelPresent bool) (uint32, error) {
	// Note: for every HostedCluster, we *either* expect to see the HCCO report the number of nodes into the
	// HostedControlPlane status, *or* we must walk NodePools and count up their replicas here. We need to
	// determine which of the cases we're in by looking at what the HCCO supports, and we cannot simply look
	// at the HostedControlPlane status, as we may race, which could land us in the following unpleasant case:
	// - this controller reconciles a new HostedCluster, uses NodePools as the source of truth for size
	// - this controller adds a large t-shirt size, we scale up the request serving nodes
	// - the HCCO finishes processing and reports some other number of nodes, using Nodes as the source of truth
	// - this controller re-processes and transitions the cluster to a different t-shirt size, causing churn on the
	//   request serving nodes
	hccoReportsNodeCount, err := r.hccoReportsNodeCount(ctx, hostedCluster)
	if err != nil {
		return 0, fmt.Errorf("failed to determine if HCCO reports node count: %w", err)
	}

	// Determine if the Kube API Server is available to determine if we can trust the node count from nodepool.status.replicas
	// If the Kube API Server is not available, we cannot trust the node count from nodepool.status.replicas
	// Ref: kubernetes-sigs/cluster-api#10195
	kasAvailableCondition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(hypershiftv1beta1.KubeAPIServerAvailable))
	kasAvailable := kasAvailableCondition != nil && kasAvailableCondition.Status == metav1.ConditionTrue

	// first, we figure out the node count for the hosted cluster
	var nodeCount uint32
	if hccoReportsNodeCount {
		hostedControlPlane, err := r.hostedControlPlaneForHostedCluster(ctx, hostedCluster)
		if err != nil {
			return 0, ignoreError(fmt.Errorf("failed to get hosted control plane: %w", err))
		}

		if hostedControlPlane.Status.NodeCount != nil && *hostedControlPlane.Status.NodeCount > 0 {
			nodeCount = uint32(*hostedControlPlane.Status.NodeCount)
		}
	} else {
		nodePools, err := r.nodePoolsForHostedCluster(ctx, hostedCluster)
		if err != nil {
			return 0, err
		}

		for _, nodePool := range nodePools.Items {
			var replicas uint32
			// If autoscaling, the replicas should be returned from status
			if nodePool.Spec.AutoScaling != nil {
				// If the Kube API Server is not available, and we already have a size label, skip processing
				if !kasAvailable && sizeClassLabelPresent {
					return 0, ignoreError(fmt.Errorf("KAS is not available, and no size class label is set yet"))
				}
				replicas = uint32(nodePool.Status.Replicas)
			} else if nodePool.Spec.Replicas != nil {
				replicas = uint32(*nodePool.Spec.Replicas)
			}
			nodeCount += replicas
		}
	}
	return nodeCount, nil
}

// transitionsWithinSlidingWindow determines the number of hosted clusters that have transitioned within the sliding
// window from now; returning both the count of transitions and the duration until the count will change next
func transitionsWithinSlidingWindow(hostedClusters *hypershiftv1beta1.HostedClusterList, slidingWindow time.Duration, now time.Time) (int, time.Duration) {
	cutoff := now.Add(-slidingWindow)
	var withinWindow int
	oldestTransition := now
	for _, hostedCluster := range hostedClusters.Items {
		lastTransitionTime, _ := previousTransitionFor(&hostedCluster)
		if lastTransitionTime != nil && (*lastTransitionTime).After(cutoff) {
			withinWindow++
			if (*lastTransitionTime).Before(oldestTransition) {
				oldestTransition = *lastTransitionTime
			}
		}
	}
	return withinWindow, oldestTransition.Add(slidingWindow).Sub(now)
}

func previousTransitionFor(hostedCluster *hypershiftv1beta1.HostedCluster) (*time.Time, string) {
	for i, condition := range hostedCluster.Status.Conditions {
		if condition.Type == hypershiftv1beta1.ClusterSizeComputed && condition.Status == metav1.ConditionTrue {
			return &hostedCluster.Status.Conditions[i].LastTransitionTime.Time, hostedCluster.Status.Conditions[i].Reason
		}
	}
	return nil, ""
}

func previousComputedSizeFor(hostedCluster *hypershiftv1beta1.HostedCluster) (*time.Time, string) {
	for i, condition := range hostedCluster.Status.Conditions {
		if condition.Type == hypershiftv1beta1.ClusterSizeTransitionRequired && condition.Status == metav1.ConditionTrue {
			return &hostedCluster.Status.Conditions[i].LastTransitionTime.Time, hostedCluster.Status.Conditions[i].Reason
		}
	}
	return nil, ""
}

func applyCfgFor(hostedCluster *hypershiftv1beta1.HostedCluster, updated ...*metav1applyconfigurations.ConditionApplyConfiguration) *hypershiftv1beta1applyconfigurations.HostedClusterApplyConfiguration {
	var toUpdate []*metav1applyconfigurations.ConditionApplyConfiguration
	for _, condition := range updated {
		if !conditionPresent(hostedCluster, *condition.Type, *condition.Status, *condition.Reason, *condition.Message) {
			toUpdate = append(toUpdate, condition)
		}
	}
	if len(toUpdate) == 0 {
		return nil
	}

	return hypershiftv1beta1applyconfigurations.HostedCluster(hostedCluster.Name, hostedCluster.Namespace).
		WithStatus(
			hypershiftv1beta1applyconfigurations.HostedClusterStatus().
				WithConditions(
					conditions(hostedCluster.Status.Conditions, toUpdate...)...,
				),
		)
}

func conditionPresent(hostedCluster *hypershiftv1beta1.HostedCluster, conditionType string, status metav1.ConditionStatus, reason, message string) bool {
	for _, condition := range hostedCluster.Status.Conditions {
		if condition.Type == conditionType && condition.Status == status && condition.Reason == reason && condition.Message == message {
			return true
		}
	}
	return false
}

var managedConditions = sets.New[string](hypershiftv1beta1.ClusterSizeComputed, hypershiftv1beta1.ClusterSizeTransitionPending, hypershiftv1beta1.ClusterSizeTransitionRequired)

// conditions provides the full list of conditions that we need to send with each SSA call -
// if one field manager sets some conditions in one call, and another set in a second, any conditions
// provided in the first but not the second will be removed. Therefore, we need to provide the whole
// list of conditions this controller manages on each call. Since we are not the only actor to add
// conditions to this resource, we must accumulate only the conditions we control and simply append
// the new one, or overwrite a current condition if we're updating the content for that type.
func conditions(existing []metav1.Condition, updated ...*metav1applyconfigurations.ConditionApplyConfiguration) []*metav1applyconfigurations.ConditionApplyConfiguration {
	updatedTypes := sets.New[string]()
	for _, condition := range updated {
		if condition.Type == nil {
			panic(fmt.Errorf("programmer error: must set a type for condition: %#v", condition))
		}
		if !managedConditions.Has(*condition.Type) {
			panic(fmt.Errorf("programmer error: attempting to set unmanaged condition type %q", *condition.Type))
		}
		updatedTypes.Insert(*condition.Type)
	}
	conditions := updated
	for _, condition := range existing {
		if !updatedTypes.Has(condition.Type) && managedConditions.Has(condition.Type) {
			conditions = append(conditions, metav1applyconfigurations.Condition().
				WithType(condition.Type).
				WithStatus(condition.Status).
				WithObservedGeneration(condition.ObservedGeneration).
				WithLastTransitionTime(condition.LastTransitionTime).
				WithReason(condition.Reason).
				WithMessage(condition.Message),
			)
		}
	}
	sort.Slice(conditions, func(i, j int) bool {
		return *conditions[i].Type < *conditions[j].Type
	})
	return conditions
}
