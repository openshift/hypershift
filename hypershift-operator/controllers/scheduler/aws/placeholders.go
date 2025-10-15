package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	appsv1applyconfigurations "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PlaceholderScheduler struct{}

const (
	placeholderNamespace      = "hypershift-request-serving-node-placeholders"
	placeholderControllerName = "PlaceholderScheduler"
	defaultPlaceholderImage   = "registry.access.redhat.com/ubi8/pause:latest"
)

func (r *PlaceholderScheduler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	kubernetesClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	if _, err := kubernetesClient.CoreV1().Namespaces().Apply(ctx, corev1applyconfigurations.Namespace(placeholderNamespace), metav1.ApplyOptions{FieldManager: placeholderControllerName}); err != nil {
		return fmt.Errorf("couldn't set up namespace: %w", err)
	}

	lister := &placeholderLister{
		getClusterSizingConfiguration: func(ctx context.Context) (*schedulingv1alpha1.ClusterSizingConfiguration, error) {
			config := schedulingv1alpha1.ClusterSizingConfiguration{}
			if err := mgr.GetClient().Get(ctx, types.NamespacedName{Name: "cluster"}, &config); err != nil {
				return nil, fmt.Errorf("could not get cluster sizing configuration: %w", err)
			}
			return &config, nil
		},
		getDeployment: func(ctx context.Context, name types.NamespacedName) (*appsv1.Deployment, error) {
			deployment := appsv1.Deployment{}
			if err := mgr.GetClient().Get(ctx, name, &deployment); err != nil {
				return nil, fmt.Errorf("could not get deployment: %w", err)
			}
			return &deployment, nil
		},
		listDeployments: func(ctx context.Context, opts ...client.ListOption) (*appsv1.DeploymentList, error) {
			deployments := appsv1.DeploymentList{}
			if err := mgr.GetClient().List(ctx, &deployments, opts...); err != nil {
				return nil, fmt.Errorf("could not list deployments: %w", err)
			}
			return &deployments, nil
		},
		listConfigMaps: func(ctx context.Context, opts ...client.ListOption) (*corev1.ConfigMapList, error) {
			configMaps := corev1.ConfigMapList{}
			if err := mgr.GetClient().List(ctx, &configMaps, opts...); err != nil {
				return nil, fmt.Errorf("could not list configmaps: %w", err)
			}
			return &configMaps, nil
		},
	}

	// the placeholderCreator mints new placeholder deployments as necessary
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&schedulingv1alpha1.ClusterSizingConfiguration{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(equeueClusterSizingConfigForPlaceholderResource)).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(equeueClusterSizingConfigForPlaceholderResource)).
		Named(placeholderControllerName + ".Creator").Complete(&placeholderCreator{
		client:            kubernetesClient,
		placeholderLister: lister,
	}); err != nil {
		return err
	}

	// the placeholderUpdater ensures that existing deployments are up-to-date with their config and that excess deployments are deleted
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Watches(&schedulingv1alpha1.ClusterSizingConfiguration{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			// when the sizing configuration changes, we need to re-process our Deployments to make sure we have the right number
			deployments := appsv1.DeploymentList{}
			if err := mgr.GetClient().List(ctx, &deployments, client.HasLabels{PlaceholderLabel}); err != nil {
				mgr.GetLogger().Error(err, "failed to list deployments when enqueuing for sizing configuration change")
				return nil
			}
			var out []reconcile.Request
			for _, deployment := range deployments.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}})
			}
			return out
		})).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			// when a node changes, we need to re-process every Deployment to update the list of paired nodes labels
			deployments := appsv1.DeploymentList{}
			if err := mgr.GetClient().List(ctx, &deployments, client.HasLabels{PlaceholderLabel}); err != nil {
				mgr.GetLogger().Error(err, "failed to list deployments when enqueuing for sizing configuration change")
				return nil
			}
			var out []reconcile.Request
			for _, deployment := range deployments.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}})
			}
			return out
		})).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 10,
		}).Named(placeholderControllerName + ".Updater").Complete(&placeholderUpdater{
		client:            kubernetesClient,
		placeholderLister: lister,
	}); err != nil {
		return err
	}

	return nil
}

func equeueClusterSizingConfigForPlaceholderResource(ctx context.Context, d client.Object) []reconcile.Request {
	if d.GetNamespace() != placeholderNamespace {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "cluster"}}}
}

type placeholderLister struct {
	getClusterSizingConfiguration func(context.Context) (*schedulingv1alpha1.ClusterSizingConfiguration, error)
	getDeployment                 func(context.Context, types.NamespacedName) (*appsv1.Deployment, error)
	listDeployments               func(context.Context, ...client.ListOption) (*appsv1.DeploymentList, error)
	listConfigMaps                func(context.Context, ...client.ListOption) (*corev1.ConfigMapList, error)
}

type placeholderCreator struct {
	client kubernetes.Interface
	*placeholderLister
}

func (r *placeholderCreator) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	config, err := r.getClusterSizingConfiguration(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}

	deployment, err := r.reconcile(ctx, config)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deployment != nil {
		_, err := r.client.AppsV1().Deployments(placeholderNamespace).Apply(ctx, deployment, metav1.ApplyOptions{FieldManager: placeholderControllerName})
		return reconcile.Result{Requeue: true}, err
	}
	return reconcile.Result{}, nil
}

func (r *placeholderCreator) reconcile(
	ctx context.Context,
	config *schedulingv1alpha1.ClusterSizingConfiguration,
) (*appsv1applyconfigurations.DeploymentApplyConfiguration, error) {
	logger := ctrl.LoggerFrom(ctx)

	if condition := meta.FindStatusCondition(config.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		// we can't deliver placeholders unless we have a valid configuration; we'll re-trigger when
		// the configuration object changes and can process deployments then
		return nil, nil
	}

	for _, sizeClass := range config.Spec.Sizes {
		if sizeClass.Management != nil && sizeClass.Management.Placeholders != 0 {
			deployments, err := r.listDeployments(ctx, client.InNamespace(placeholderNamespace), client.HasLabels{PlaceholderLabel}, client.MatchingLabels{hypershiftv1beta1.HostedClusterSizeLabel: sizeClass.Name})
			if err != nil {
				return nil, err
			}
			logger.WithValues("size", sizeClass.Name, "expected", sizeClass.Management.Placeholders, "got", len(deployments.Items)).Info("resolving placeholders")

			if len(deployments.Items) >= sizeClass.Management.Placeholders {
				// we already have all the placeholders we need, nothing to do
				return nil, nil
			}

			pairLabelsAssignedToHostedClusters := sets.Set[string]{}
			assignedConfigMaps, err := r.listConfigMaps(ctx, client.HasLabels{pairLabelKey}, client.InNamespace(placeholderNamespace))
			if err != nil {
				return nil, err
			}
			for _, cm := range assignedConfigMaps.Items {
				pairLabel := cm.Labels[pairLabelKey]
				if pairLabel != "" {
					pairLabelsAssignedToHostedClusters.Insert(pairLabel)
				}
			}

			// which placeholder are we missing?
			presentIndices := sets.Set[int]{}
			for _, deployment := range deployments.Items {
				index, err := parseIndex(deployment.ObjectMeta.Labels[hypershiftv1beta1.HostedClusterSizeLabel], deployment.ObjectMeta.Name)
				if err != nil {
					// this should never happen, but we can't progress if it does
					logger.Error(err, "deployment has invalid placeholder index value", "value", deployment.ObjectMeta.Name)
					return nil, nil
				}
				// the indices are names of k8s resources, so we know they won't collide,
				// so the use of a set is for an easy .Has() and not for deduplication
				presentIndices.Insert(index)
			}
			missingIndex := -1
			for i := 0; i < sizeClass.Management.Placeholders; i++ {
				if !presentIndices.Has(i) {
					missingIndex = i
					break
				}
			}
			if missingIndex == -1 {
				// should not happen, we checked that len(deployments.Items) < sizeClass.Management.Placeholders
				logger.Error(fmt.Errorf("no missing indices found"), "logic error detected")
				return nil, nil
			}

			return newDeployment(placeholderNamespace, sizeClass.Name, missingIndex, pairLabelsAssignedToHostedClusters.UnsortedList()), nil
		}
	}

	return nil, nil
}

type placeholderUpdater struct {
	client kubernetes.Interface
	*placeholderLister
}

func (r *placeholderUpdater) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	deployment, err := r.getDeployment(ctx, req.NamespacedName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if deployment.Namespace != placeholderNamespace {
		return reconcile.Result{}, err
	}

	config, err := r.getClusterSizingConfiguration(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}

	shouldDelete, update, err := r.reconcile(ctx, deployment, config)
	if err != nil {
		return reconcile.Result{}, err
	}

	if shouldDelete {
		return reconcile.Result{}, r.client.AppsV1().Deployments(placeholderNamespace).Delete(ctx, req.Name, metav1.DeleteOptions{})
	}
	if update != nil {
		_, err := r.client.AppsV1().Deployments(placeholderNamespace).Apply(ctx, update, metav1.ApplyOptions{FieldManager: placeholderControllerName})
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *placeholderUpdater) reconcile(
	ctx context.Context,
	deployment *appsv1.Deployment,
	config *schedulingv1alpha1.ClusterSizingConfiguration,
) (bool, *appsv1applyconfigurations.DeploymentApplyConfiguration, error) {
	logger := ctrl.LoggerFrom(ctx)

	_, isPlaceholder := deployment.ObjectMeta.Labels[PlaceholderLabel]
	placeholderSize, hasSize := deployment.ObjectMeta.Labels[hypershiftv1beta1.HostedClusterSizeLabel]

	if !isPlaceholder || !hasSize {
		return false, nil, nil
	}

	var configValid bool
	for _, condition := range config.Status.Conditions {
		if condition.Type == schedulingv1alpha1.ClusterSizingConfigurationValidType && condition.Status == metav1.ConditionTrue {
			configValid = true
			break
		}
	}
	if !configValid {
		// we can't deliver placeholders unless we have a valid configuration; we'll re-trigger when
		// the configuration object changes and can process deployments then
		return false, nil, nil
	}

	var wantedDeployments int
	for _, sizeClass := range config.Spec.Sizes {
		if sizeClass.Name == placeholderSize && sizeClass.Management != nil {
			wantedDeployments = sizeClass.Management.Placeholders
		}
	}

	parsedIndex, err := parseIndex(placeholderSize, deployment.ObjectMeta.Name)
	if err != nil {
		logger.Error(err, "deployment has invalid placeholder index value", "value", deployment.ObjectMeta.Name)
		return false, nil, nil
	}

	// index is zero-based, user intent is 1-based
	if parsedIndex > wantedDeployments-1 {
		// we have more deployments than we need, delete this one
		return true, nil, nil
	}

	pairLabelsAssignedToHostedClusters := sets.Set[string]{}
	assignedConfigMaps, err := r.listConfigMaps(ctx, client.HasLabels{pairLabelKey}, client.InNamespace(placeholderNamespace))
	if err != nil {
		return false, nil, err
	}
	for _, cm := range assignedConfigMaps.Items {
		pairLabel := cm.Labels[pairLabelKey]
		if pairLabel != "" {
			pairLabelsAssignedToHostedClusters.Insert(pairLabel)
		}
	}

	existingPairLabels := sets.Set[string]{}
	if affinity := deployment.Spec.Template.Spec.Affinity; affinity != nil {
		if nodeAffinity := affinity.NodeAffinity; nodeAffinity != nil {
			if selector := nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution; selector != nil {
				for _, term := range selector.NodeSelectorTerms {
					for _, expression := range term.MatchExpressions {
						if expression.Key == OSDFleetManagerPairedNodesLabel && expression.Operator == corev1.NodeSelectorOpNotIn {
							existingPairLabels.Insert(expression.Values...)
						}
					}
				}
			}
		}
	}

	if !pairLabelsAssignedToHostedClusters.Equal(existingPairLabels) {
		// the set of paired nodes on which hosted clusters have been scheduled has changed, we need to update the
		// node affinity configuration on the deployment
		return false, newDeployment(placeholderNamespace, placeholderSize, parsedIndex, pairLabelsAssignedToHostedClusters.UnsortedList()), nil
	}
	return false, nil, nil
}

func deploymentName(sizeClass string, index int) string {
	return fmt.Sprintf("placeholder-%s-%d", sizeClass, index)
}

func parseIndex(sizeClass, name string) (int, error) {
	expectedPrefix := fmt.Sprintf("placeholder-%s-", sizeClass)
	if !strings.HasPrefix(name, expectedPrefix) {
		return 0, fmt.Errorf("deployment %q has invalid format - expected a %q prefix", name, expectedPrefix)
	}

	return strconv.Atoi(strings.TrimPrefix(name, expectedPrefix))
}

func newDeployment(namespace, sizeClass string, placeholderIndex int, pairedNodes []string) *appsv1applyconfigurations.DeploymentApplyConfiguration {
	var nodeAffinity *corev1applyconfigurations.NodeAffinityApplyConfiguration
	if len(pairedNodes) > 0 {
		sort.Strings(pairedNodes)
		// we can't add this unless there's something for the NotIn to match
		nodeAffinity = corev1applyconfigurations.NodeAffinity().WithRequiredDuringSchedulingIgnoredDuringExecution(
			corev1applyconfigurations.NodeSelector().WithNodeSelectorTerms(
				// placeholder pods may not land on any nodes where other hosted clusters may be scheduled, even if
				// the hosted control plane is currently not currently there - for instance, if hosted cluster is
				// a 'large' size, we don't want placeholders landing on the 'small' nodes that the cluster would use
				// if it were to scale down, since keeping those warm does not help us start new clusters more quickly
				corev1applyconfigurations.NodeSelectorTerm().WithMatchExpressions(
					corev1applyconfigurations.NodeSelectorRequirement().
						WithKey(OSDFleetManagerPairedNodesLabel).
						WithOperator(corev1.NodeSelectorOpNotIn).
						WithValues(pairedNodes...),
				),
			),
		)
	}
	return appsv1applyconfigurations.Deployment(deploymentName(sizeClass, placeholderIndex), namespace).WithLabels(map[string]string{
		PlaceholderLabel:                         strconv.Itoa(placeholderIndex),
		hypershiftv1beta1.HostedClusterSizeLabel: sizeClass,
	}).WithSpec(
		appsv1applyconfigurations.DeploymentSpec().
			WithReplicas(2).
			WithSelector(metav1applyconfigurations.LabelSelector().WithMatchLabels(map[string]string{
				PlaceholderLabel:                         strconv.Itoa(placeholderIndex),
				hypershiftv1beta1.HostedClusterSizeLabel: sizeClass,
			})).
			WithStrategy(appsv1applyconfigurations.DeploymentStrategy().
				WithType(appsv1.RecreateDeploymentStrategyType)).
			WithTemplate(corev1applyconfigurations.PodTemplateSpec().
				WithLabels(map[string]string{
					PlaceholderLabel:                         strconv.Itoa(placeholderIndex),
					hypershiftv1beta1.HostedClusterSizeLabel: sizeClass,
				}).
				WithSpec(corev1applyconfigurations.PodSpec().
					// placeholder pods must land on request serving nodes for the size class we're keeping warm
					WithNodeSelector(map[string]string{
						ControlPlaneServingComponentLabel: "true",
						hypershiftv1beta1.NodeSizeLabel:   sizeClass,
					}).
					WithAffinity(corev1applyconfigurations.Affinity().
						WithPodAffinity(corev1applyconfigurations.PodAffinity().WithRequiredDuringSchedulingIgnoredDuringExecution(
							// placeholder pods must land on nodes with matching paired-nodes labels
							corev1applyconfigurations.PodAffinityTerm().WithLabelSelector(metav1applyconfigurations.LabelSelector().WithMatchExpressions(
								metav1applyconfigurations.LabelSelectorRequirement().
									WithKey(PlaceholderLabel).
									WithOperator(metav1.LabelSelectorOpIn).
									WithValues(strconv.Itoa(placeholderIndex)),
							)).WithTopologyKey(OSDFleetManagerPairedNodesLabel),
						)).
						WithPodAntiAffinity(corev1applyconfigurations.PodAntiAffinity().WithRequiredDuringSchedulingIgnoredDuringExecution(
							// placeholder pods must land in different zones
							corev1applyconfigurations.PodAffinityTerm().WithLabelSelector(metav1applyconfigurations.LabelSelector().WithMatchExpressions(
								metav1applyconfigurations.LabelSelectorRequirement().
									WithKey(PlaceholderLabel).
									WithOperator(metav1.LabelSelectorOpIn).
									WithValues(strconv.Itoa(placeholderIndex)),
							)).WithTopologyKey("topology.kubernetes.io/zone"),
							// placeholders pods for this deployment can't land on a node where other placeholders already exist
							corev1applyconfigurations.PodAffinityTerm().WithLabelSelector(metav1applyconfigurations.LabelSelector().WithMatchExpressions(
								metav1applyconfigurations.LabelSelectorRequirement().
									WithKey(PlaceholderLabel).
									WithOperator(metav1.LabelSelectorOpExists),
							)).WithTopologyKey("kubernetes.io/hostname"),
							corev1applyconfigurations.PodAffinityTerm().WithLabelSelector(metav1applyconfigurations.LabelSelector().WithMatchExpressions(
								metav1applyconfigurations.LabelSelectorRequirement().
									WithKey(PlaceholderLabel).
									WithOperator(metav1.LabelSelectorOpNotIn).
									WithValues(strconv.Itoa(placeholderIndex)),
							)).WithTopologyKey(OSDFleetManagerPairedNodesLabel),
						)).
						WithNodeAffinity(nodeAffinity),
					).
					// placeholder pods must tolerate landing on a request-serving node
					WithTolerations(corev1applyconfigurations.Toleration().
						WithKey(ControlPlaneServingComponentTaint).
						WithOperator(corev1.TolerationOpEqual).
						WithValue("true").
						WithEffect(corev1.TaintEffectNoSchedule),
					).
					WithTolerations(corev1applyconfigurations.Toleration().
						WithKey(ControlPlaneTaint).
						WithOperator(corev1.TolerationOpEqual).
						WithValue("true").
						WithEffect(corev1.TaintEffectNoSchedule),
					).
					WithContainers(corev1applyconfigurations.Container().
						WithName("placeholder").
						WithImage(defaultPlaceholderImage),
					),
				),
			),
	)
}
