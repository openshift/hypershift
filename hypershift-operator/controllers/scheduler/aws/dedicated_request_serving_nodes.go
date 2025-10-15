package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	schedulerutil "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/util"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControlPlaneTaint                 = "hypershift.openshift.io/control-plane"
	ControlPlaneServingComponentTaint = "hypershift.openshift.io/request-serving-component"
	HostedClusterTaint                = "hypershift.openshift.io/cluster"

	ControlPlaneServingComponentLabel = "hypershift.openshift.io/request-serving-component"
	OSDFleetManagerPairedNodesLabel   = "osd-fleet-manager.openshift.io/paired-nodes"
	HostedClusterNameLabel            = "hypershift.openshift.io/cluster-name"
	HostedClusterNamespaceLabel       = "hypershift.openshift.io/cluster-namespace"

	// PlaceholderLabel is used as a label on Deployments that are used to keep nodes warm.
	PlaceholderLabel = "hypershift.openshift.io/placeholder"

	autoSizerNamespace = placeholderNamespace

	clusterNamespaceKey = "clusterNamespace"
	clusterNameKey      = "clusterName"
	pairLabelKey        = "hypershift.openshift.io/pairlabel"
	schedulerFinalizer  = "hypershift.openshift.io/dedicated-request-serving-scheduler"
)

type DedicatedServingComponentNodeReaper struct {
	client.Client
}

func (r *DedicatedServingComponentNodeReaper) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&hyperv1.HostedCluster{}, handler.EnqueueRequestsFromMapFunc(mapHostedClusterToNodesFn(r.Client, mgr))).
		Named("DedicatedServingComponentNodeReaper")
	return builder.Complete(r)
}

func mapHostedClusterToNodesFn(c client.Client, mgr ctrl.Manager) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		// when a HostedCluster changes, queue the nodes for it
		nodes := &corev1.NodeList{}
		if err := c.List(ctx, nodes,
			client.HasLabels{hyperv1.RequestServingComponentLabel},
			client.MatchingLabels{
				hyperv1.HostedClusterLabel:  fmt.Sprintf("%s-%s", object.GetNamespace(), object.GetName()),
				HostedClusterNamespaceLabel: object.GetNamespace(),
				HostedClusterNameLabel:      object.GetName(),
			}); err != nil {
			mgr.GetLogger().Error(err, "failed to list nodes when enqueuing for hosted cluster")
			return nil
		}
		var out []reconcile.Request
		for _, node := range nodes.Items {
			out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: node.Namespace, Name: node.Name}})
		}
		return out
	}
}

func (r *DedicatedServingComponentNodeReaper) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx, "node", req.Name)
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, aborting reconcile", "name", req.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get node %q: %w", req.NamespacedName.String(), err)
	}

	if _, hasServingComponentLabel := node.Labels[hyperv1.RequestServingComponentLabel]; !hasServingComponentLabel {
		return ctrl.Result{}, nil
	}

	if _, hasHostedClusterLabel := node.Labels[hyperv1.HostedClusterLabel]; !hasHostedClusterLabel {
		return ctrl.Result{}, nil
	}

	name := node.Labels[HostedClusterNameLabel]
	namespace := node.Labels[HostedClusterNamespaceLabel]
	hc := &hyperv1.HostedCluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster %s/%s: %w", namespace, name, err)
		}
		log.Info("Hosted cluster is not found for node. Deleting node.")
		if err := r.Delete(ctx, node); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete node: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

type DedicatedServingComponentScheduler struct {
	client.Client
	createOrUpdate upsert.CreateOrUpdateFN
}

func (r *DedicatedServingComponentScheduler) SetupWithManager(mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {

	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}, builder.WithPredicates(util.PredicatesForHostedClusterAnnotationScoping(mgr.GetClient()))).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).Named("DedicatedServingComponentScheduler")
	return builder.Complete(r)
}

func (r *DedicatedServingComponentScheduler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hcluster := &hyperv1.HostedCluster{}
	log := ctrl.LoggerFrom(ctx, "hostedcluster", req.NamespacedName.String())
	err := r.Get(ctx, req.NamespacedName, hcluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}
	if !hcluster.DeletionTimestamp.IsZero() {
		log.Info("hostedcluster is deleted, nothing to do")
		return ctrl.Result{}, nil
	}
	if hcTopology := hcluster.Annotations[hyperv1.TopologyAnnotation]; hcTopology != hyperv1.DedicatedRequestServingComponentsTopology {
		log.Info("hostedcluster does not use isolated request serving components, nothing to do")
		return ctrl.Result{}, nil
	}

	// Find existing dedicated serving content Nodes for this HC.
	dedicatedNodesForHC := &corev1.NodeList{}
	if err := r.List(ctx, dedicatedNodesForHC,
		client.HasLabels{hyperv1.RequestServingComponentLabel},
		client.MatchingLabels{
			hyperv1.HostedClusterLabel: fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name),
		}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(dedicatedNodesForHC.Items) > 2 {
		return ctrl.Result{}, fmt.Errorf("found too many dedicated nodes for HC: %v", len(dedicatedNodesForHC.Items))
	}

	// We check existing dedicated Nodes are 2. If not e.g. some was deleted, continue.
	if scheduled := hcluster.Annotations[hyperv1.HostedClusterScheduledAnnotation]; scheduled == "true" && len(dedicatedNodesForHC.Items) == 2 {
		log.Info("hosted cluster is already scheduled, nothing to do")
		return ctrl.Result{}, nil
	}

	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList, client.HasLabels{hyperv1.RequestServingComponentLabel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodesToUse := map[string]*corev1.Node{}
	// first, find any existing nodes already labeled for this hostedcluster
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if !node.DeletionTimestamp.IsZero() {
			// Skip nodes that are being deleted
			continue
		}
		zone, hasZoneLabel := node.Labels["topology.kubernetes.io/zone"]
		if !hasZoneLabel {
			continue
		}
		hcLabel, hasHCLabel := node.Labels[hyperv1.HostedClusterLabel]
		if !hasHCLabel {
			continue
		}
		if hcLabel == fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name) {
			nodesToUse[zone] = node
			log.Info("Found existing node for hosted cluster", "node", node.Name, "zone", zone)
		}
	}

	if len(nodesToUse) < 2 {
		for i := range nodeList.Items {
			node := &nodeList.Items[i]
			zone, hasZoneLabel := node.Labels["topology.kubernetes.io/zone"]
			if !hasZoneLabel {
				// No zone has been set on the node, we cannot use it
				continue
			}

			_, hasHCLabel := node.Labels[hyperv1.HostedClusterLabel]
			if hasHCLabel {
				// The node has been allocated to a different hosted cluster, skip it
				continue
			}

			if nodesToUse[zone] == nil {

				// if the candidate Node is not paired with the existing node to use then skip.
				paired := false
				if len(nodesToUse) > 0 {
					for _, n := range nodesToUse {
						if n.Labels[OSDFleetManagerPairedNodesLabel] == node.Labels[OSDFleetManagerPairedNodesLabel] {
							paired = true
						}
					}
					if !paired {
						continue
					}
				}

				log.Info("Found node to allocate for hosted cluster", "node", node.Name, "zone", zone)
				nodesToUse[zone] = node
			}

			if len(nodesToUse) == 2 {
				break
			}
		}
	}
	if len(nodesToUse) < 2 {
		return ctrl.Result{}, fmt.Errorf("failed to find enough available nodes for cluster, found %d", len(nodesToUse))
	}

	nodeGoMemLimit := ""
	lbSubnets := ""
	pairLabel := ""
	for _, node := range nodesToUse {
		originalNode := node.DeepCopy()

		if node.Labels[schedulerutil.GoMemLimitLabel] != "" && nodeGoMemLimit == "" {
			nodeGoMemLimit = node.Labels[schedulerutil.GoMemLimitLabel]
		}
		if node.Labels[schedulerutil.LBSubnetsLabel] != "" && lbSubnets == "" {
			lbSubnets = node.Labels[schedulerutil.LBSubnetsLabel]
			// If subnets are separated by periods, replace them with commas
			lbSubnets = strings.ReplaceAll(lbSubnets, ".", ",")
		}
		if node.Labels[OSDFleetManagerPairedNodesLabel] != "" && pairLabel == "" {
			pairLabel = node.Labels[OSDFleetManagerPairedNodesLabel]
		}

		// Add taint and labels for specific hosted cluster
		hasTaint := false
		hcNameValue := fmt.Sprintf("%s-%s", hcluster.Namespace, hcluster.Name)
		for i := range node.Spec.Taints {
			if node.Spec.Taints[i].Key == HostedClusterTaint {
				node.Spec.Taints[i].Value = hcNameValue
				node.Spec.Taints[i].Effect = corev1.TaintEffectNoSchedule
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
				Key:    HostedClusterTaint,
				Value:  hcNameValue,
				Effect: corev1.TaintEffectNoSchedule,
			})
		}
		node.Labels[hyperv1.HostedClusterLabel] = hcNameValue
		node.Labels[HostedClusterNameLabel] = hcluster.Name
		node.Labels[HostedClusterNamespaceLabel] = hcluster.Namespace

		if err := r.Patch(ctx, node, client.MergeFromWithOptions(originalNode, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update labels and taints on node %s: %w", node.Name, err)
		}
		log.Info("Node tainted and labeled for hosted cluster", "node", node.Name)
	}

	// finally update HostedCluster with new annotation
	log.Info("Setting scheduled annotation on hosted cluster")
	originalHcluster := hcluster.DeepCopy()
	hcluster.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
	if nodeGoMemLimit != "" {
		hcluster.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation] = nodeGoMemLimit
	}
	if lbSubnets != "" {
		hcluster.Annotations[hyperv1.AWSLoadBalancerSubnetsAnnotation] = lbSubnets
	}
	if pairLabel != "" {
		hcluster.Annotations[hyperv1.AWSLoadBalancerTargetNodesAnnotation] =
			fmt.Sprintf("%s=%s", OSDFleetManagerPairedNodesLabel, pairLabel)
	}
	if err := r.Patch(ctx, hcluster, client.MergeFrom(originalHcluster)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update hostedcluster annotation: %w", err)
	}

	return ctrl.Result{}, nil
}

const requestServingSchedulerAndSizerName = "DedicatedServingComponentSchedulerAndSizer"

type DedicatedServingComponentSchedulerAndSizer struct {
	client.Client
	createOrUpdate upsert.CreateOrUpdateFN
}

func (r *DedicatedServingComponentSchedulerAndSizer) SetupWithManager(ctx context.Context, mgr ctrl.Manager, createOrUpdateProvider upsert.CreateOrUpdateProvider) error {
	r.Client = mgr.GetClient()
	r.createOrUpdate = createOrUpdateProvider.CreateOrUpdate
	kubernetesClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	if _, err := kubernetesClient.CoreV1().Namespaces().Apply(ctx, corev1applyconfigurations.Namespace(autoSizerNamespace), metav1.ApplyOptions{FieldManager: requestServingSchedulerAndSizerName}); err != nil {
		return fmt.Errorf("couldn't set up namespace: %w", err)
	}
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.filterNodeEvents)).
		Watches(&schedulingv1alpha1.ClusterSizingConfiguration{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			hostedClusters := &hyperv1.HostedClusterList{}
			if err := r.List(ctx, hostedClusters); err != nil {
				return nil
			}
			var out []reconcile.Request
			for _, hc := range hostedClusters.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: hc.Namespace, Name: hc.Name}})
			}
			return out
		})).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			deployment := obj.(*appsv1.Deployment)
			if deployment.Namespace != autoSizerNamespace {
				return nil
			}
			name := deployment.Labels[HostedClusterNameLabel]
			namespace := deployment.Labels[HostedClusterNamespaceLabel]
			if name == "" || namespace == "" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
		})).
		Named(requestServingSchedulerAndSizerName)
	return builder.Complete(r)
}

func (r *DedicatedServingComponentSchedulerAndSizer) filterNodeEvents(ctx context.Context, obj client.Object) []reconcile.Request {
	node := obj.(*corev1.Node)
	if _, isReqServing := node.Labels[hyperv1.RequestServingComponentLabel]; !isReqServing {
		return nil
	}
	if _, hasHCLabel := node.Labels[hyperv1.HostedClusterLabel]; !hasHCLabel {
		// If a node is labeled with RequestServingComponentLabel, but not with HostedClusterLabel, check for
		// its paired node by the OSDFleetManagerPairedNodesLabel. It could be that a hostedcluster has
		// previously been scheduled to this pair of nodes, but a node was recreated for some reason.
		pairLabel, isOSDFleetManagedPaired := node.Labels[OSDFleetManagerPairedNodesLabel]
		if !isOSDFleetManagedPaired {
			return nil
		}
		otherReqServingNodes := &corev1.NodeList{}
		if err := r.List(ctx, otherReqServingNodes, client.MatchingLabels{OSDFleetManagerPairedNodesLabel: pairLabel}); err != nil {
			return nil
		}

		for _, n := range otherReqServingNodes.Items {
			if _, hasHCLabel := n.Labels[hyperv1.HostedClusterLabel]; hasHCLabel {
				name := n.Labels[HostedClusterNameLabel]
				namespace := n.Labels[HostedClusterNamespaceLabel]
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
			}
		}

		return nil
	}
	name := node.Labels[HostedClusterNameLabel]
	namespace := node.Labels[HostedClusterNamespaceLabel]
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
}

func (r *DedicatedServingComponentSchedulerAndSizer) deletePairConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Deleting pair configmaps for hosted cluster", "hostedcluster", client.ObjectKeyFromObject(hc))
	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList, client.HasLabels{pairLabelKey}, client.InNamespace(placeholderNamespace)); err != nil {
		return fmt.Errorf("failed to list configmaps: %w", err)
	}
	for _, cm := range configMapList.Items {
		if cm.Data[clusterNameKey] == hc.Name && cm.Data[clusterNamespaceKey] == hc.Namespace {
			if err := r.Delete(ctx, &cm); err != nil {
				return fmt.Errorf("failed to delete configmap %s: %w", client.ObjectKeyFromObject(&cm).String(), err)
			}
		}
	}
	return nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hc := &hyperv1.HostedCluster{}
	log := ctrl.LoggerFrom(ctx)
	err := r.Get(ctx, req.NamespacedName, hc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}
	if !hc.DeletionTimestamp.IsZero() {
		log.Info("hostedcluster is deleted, cleaning up")
		if controllerutil.ContainsFinalizer(hc, schedulerFinalizer) {
			if controllerutil.ContainsFinalizer(hc, hostedcluster.HostedClusterFinalizer) {
				// Wait until the hosted cluster finalizer is removed
				return ctrl.Result{}, nil
			}
			// Ensure that any placeholder deployment is deleted
			if err := r.deletePlaceholderDeployment(ctx, hc); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.deletePairConfigMaps(ctx, hc); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(hc, schedulerFinalizer)
			if err := r.Update(ctx, hc); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}
	if hcTopology := hc.Annotations[hyperv1.TopologyAnnotation]; hcTopology != hyperv1.DedicatedRequestServingComponentsTopology {
		log.Info("hostedcluster does not use isolated request serving components, nothing to do")
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(hc, schedulerFinalizer) {
		controllerutil.AddFinalizer(hc, schedulerFinalizer)
		if err := r.Update(ctx, hc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	isPaused, duration, err := util.ProcessPausedUntilField(hc.Spec.PausedUntil, time.Now())
	if err != nil {
		log.Error(err, "error processing hosted cluster paused field")
		return ctrl.Result{}, nil // user needs to reformat the field, returning error is useless
	}
	if isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hc.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	desiredSize := hc.Labels[hyperv1.HostedClusterSizeLabel]
	if desiredSize == "" {
		log.Info("HostedCluster does not have a size label, skipping for now")
		return ctrl.Result{}, nil
	}
	log = log.WithValues("desiredSize", desiredSize)

	config := schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, &config); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not get cluster sizing configuration: %w", err)
	}

	if condition := meta.FindStatusCondition(config.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		log.Info("Cluster sizing configuration is not valid, skipping for now")
		return ctrl.Result{}, nil
	}

	// Find existing dedicated serving content Nodes for this HC.
	dedicatedNodes := &corev1.NodeList{}
	if err := r.List(ctx, dedicatedNodes,
		client.HasLabels{hyperv1.RequestServingComponentLabel},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	var goalNodes, availableNodes []corev1.Node
	var pairLabel string
	for _, node := range dedicatedNodes.Items {
		if !node.DeletionTimestamp.IsZero() {
			continue
		}
		if node.Labels[hyperv1.HostedClusterLabel] == clusterKey(hc) {
			if node.Labels[OSDFleetManagerPairedNodesLabel] != "" && pairLabel == "" {
				pairLabel = node.Labels[OSDFleetManagerPairedNodesLabel]
			}
			if node.Labels[hyperv1.NodeSizeLabel] == desiredSize && pairLabel != "" && node.Labels[OSDFleetManagerPairedNodesLabel] == pairLabel && node.DeletionTimestamp.IsZero() {
				goalNodes = append(goalNodes, node)
			}
		} else if node.Labels[hyperv1.HostedClusterLabel] == "" {
			availableNodes = append(availableNodes, node)
		}
	}
	if pairLabel == "" {
		// If no nodes were labeled, but only a configmap was created, find the pair label
		// to use from the configmaps
		pairLabel, err = r.pairLabelFromConfigMaps(ctx, hc.Namespace, hc.Name)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pair label from configmaps: %w", err)
		}
	}

	log = log.WithValues("pairLabel", pairLabel)

	// Find any nodes that are in the same fleet manager group and have the right size
	// but are not labeled with the hosted cluster label. Ensure that these nodes are labeled
	// and tainted with the hosted cluster label. This can happen if not all nodes were labeled/tainted
	// when they were initially selected.
	if pairLabel != "" {
		if err := r.ensurePairConfigMap(ctx, pairLabel, hc.Namespace, hc.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("cannot ensure pair label %s config map: %w", pairLabel, err)
		}
		var needClusterLabel []corev1.Node
		for _, node := range availableNodes {
			if node.Labels[hyperv1.NodeSizeLabel] == desiredSize && node.Labels[OSDFleetManagerPairedNodesLabel] == pairLabel {
				needClusterLabel = append(needClusterLabel, node)
			}
		}
		if len(needClusterLabel) > 0 {
			log.Info("backfilling node labels")
			for _, node := range needClusterLabel {
				if err := r.ensureHostedClusterLabelAndTaint(ctx, hc, &node); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		// If there isn't a current pair label, then we can select from available nodes selected by placeholders.
		sizeConfig := schedulerutil.SizeConfiguration(&config, desiredSize)
		if sizeConfig == nil {
			return ctrl.Result{}, fmt.Errorf("could not find size configuration for size %s", desiredSize)
		}

		// If placeholders are present, use those
		if sizeConfig.Management != nil && sizeConfig.Management.Placeholders > 0 {
			candidateNodes, err := r.nodesFromPlaceholders(ctx, desiredSize)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get nodes from placeholders: %w", err)
			}
			if len(candidateNodes) > 0 {
				pairLabel = candidateNodes[0].Labels[OSDFleetManagerPairedNodesLabel]
				if pairLabel == "" {
					return ctrl.Result{}, fmt.Errorf("node %s has no pair label", candidateNodes[0].Name)
				}
				log.WithValues("pairLabel", candidateNodes[0].Labels[OSDFleetManagerPairedNodesLabel]).Info("claiming candidate nodes")
				if err := r.ensurePairConfigMap(ctx, pairLabel, hc.Namespace, hc.Name); err != nil {
					return ctrl.Result{}, fmt.Errorf("cannot ensure pair label %s config map: %w", pairLabel, err)
				}
				for _, node := range candidateNodes {
					if err := r.ensureHostedClusterLabelAndTaint(ctx, hc, &node); err != nil {
						return ctrl.Result{}, err
					}
				}
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	nodeNamesByZone := map[string]string{}
	nodesByZone := map[string]corev1.Node{}
	for _, node := range goalNodes {
		if zone := node.Labels[corev1.LabelTopologyZone]; zone != "" {
			if _, hasNode := nodesByZone[zone]; !hasNode {
				nodesByZone[zone] = node
				nodeNamesByZone[zone] = node.Name
			}
		}
	}
	log = log.WithValues("nodes", nodeNamesByZone)

	if len(nodesByZone) > 1 {
		log.Info("sufficient nodes exist for placement")
		// If we have enough nodes, update the hosted cluster.
		if err := schedulerutil.UpdateHostedCluster(ctx, r.Client, hc, desiredSize, &config, goalNodes); err != nil {
			return ctrl.Result{}, err
		}
		// Ensure we don't have a placeholder deployment, since we have nodes
		log.Info("removing placeholder")
		if err := r.deletePlaceholderDeployment(ctx, hc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Create a deployment to ensure nodes of the right size are created
	nodesNeeded := 2 - len(nodesByZone)
	if nodesNeeded < 0 {
		nodesNeeded = 0
	}
	log.WithValues("nodesNeeded", nodesNeeded).Info("deploying placeholders")
	deployment, err := r.ensurePlaceholderDeployment(ctx, hc, desiredSize, pairLabel, nodesNeeded)
	if err != nil {
		return ctrl.Result{}, err
	}
	if deployment != nil && util.IsDeploymentReady(ctx, deployment) {
		log.Info("placeholder ready, adding node labels")
		nodes, err := r.deploymentNodes(ctx, deployment)
		if err != nil {
			return ctrl.Result{}, err
		}
		pairLabel = ""
		if len(nodes) > 0 {
			pairLabel = nodes[0].Labels[OSDFleetManagerPairedNodesLabel]
			if pairLabel == "" {
				return ctrl.Result{}, fmt.Errorf("node %s has no fleetmanager pair label", nodes[0].Name)
			}
		}
		if pairLabel == "" {
			return ctrl.Result{}, fmt.Errorf("cannot determine pair label")
		}
		if err = r.ensurePairConfigMap(ctx, pairLabel, hc.Namespace, hc.Name); err != nil {
			return ctrl.Result{}, fmt.Errorf("cannot ensure pair label %s config map: %w", pairLabel, err)
		}
		for _, node := range nodes {
			if err := r.ensureHostedClusterLabelAndTaint(ctx, hc, &node); err != nil {
				return ctrl.Result{}, err
			}
		}
		log.Info("removing placeholder")
		if err := r.deletePlaceholderDeployment(ctx, hc); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) ensurePairConfigMap(ctx context.Context, pairLabel, hcNamespace, hcName string) error {
	cfgMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: placeholderNamespace,
			Name:      pairLabel,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cfgMap), cfgMap); err != nil {
		if apierrors.IsNotFound(err) {
			cfgMap.Labels = map[string]string{
				pairLabelKey: pairLabel,
			}
			cfgMap.Data = map[string]string{
				clusterNamespaceKey: hcNamespace,
				clusterNameKey:      hcName,
			}
			if err := r.Create(ctx, cfgMap); err != nil {
				return fmt.Errorf("failed to create: %w", err)
			}
		} else {
			return fmt.Errorf("failed to read configmap: %w", err)
		}
	} else {
		if cfgMap.Data[clusterNamespaceKey] != hcNamespace || cfgMap.Data[clusterNameKey] != hcName {
			return fmt.Errorf("conflict: configmap already exists for a different cluster")
		}
	}
	return nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) pairLabelFromConfigMaps(ctx context.Context, namespace, name string) (string, error) {
	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList, client.InNamespace(placeholderNamespace), client.HasLabels{pairLabelKey}); err != nil {
		return "", err
	}
	for _, cm := range configMapList.Items {
		if cm.Data[clusterNameKey] == name && cm.Data[clusterNamespaceKey] == namespace {
			return cm.Name, nil
		}
	}
	return "", nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) nodesFromPlaceholders(ctx context.Context, size string) ([]corev1.Node, error) {
	placeHolderDeployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, placeHolderDeployments, client.InNamespace(placeholderNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list placeholder deployments: %w", err)
	}
	var deployment *appsv1.Deployment
	for i := range placeHolderDeployments.Items {
		d := &placeHolderDeployments.Items[i]
		if d.Labels[hyperv1.HostedClusterSizeLabel] != size {
			continue
		}
		if util.IsDeploymentReady(ctx, d) {
			deployment = d
			break
		}
	}
	return r.deploymentNodes(ctx, deployment)
}

func (r *DedicatedServingComponentSchedulerAndSizer) deploymentNodes(ctx context.Context, deployment *appsv1.Deployment) ([]corev1.Node, error) {
	if deployment == nil {
		return nil, nil
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(deployment.Namespace), client.MatchingLabels(deployment.Spec.Selector.MatchLabels)); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}
	var nodes []corev1.Node
	for i := range pods.Items {
		node := &corev1.Node{}
		pod := &pods.Items[i]
		if pod.Spec.NodeName == "" {
			continue
		}
		if err := r.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, node); err != nil {
			return nil, fmt.Errorf("failed to get node: %w", err)
		}
		nodes = append(nodes, *node)
	}
	return nodes, nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) ensureHostedClusterLabelAndTaint(ctx context.Context, hc *hyperv1.HostedCluster, node *corev1.Node) error {
	original := node.DeepCopy()
	foundTaint := false
	for i := range node.Spec.Taints {
		if node.Spec.Taints[i].Key == HostedClusterTaint {
			node.Spec.Taints[i].Value = clusterKey(hc)
			node.Spec.Taints[i].Effect = corev1.TaintEffectNoSchedule
			foundTaint = true
			break
		}
	}
	if !foundTaint {
		node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
			Key:    HostedClusterTaint,
			Value:  clusterKey(hc),
			Effect: corev1.TaintEffectNoSchedule,
		})
	}
	node.Labels[hyperv1.HostedClusterLabel] = clusterKey(hc)
	node.Labels[HostedClusterNameLabel] = hc.Name
	node.Labels[HostedClusterNamespaceLabel] = hc.Namespace

	if err := r.Patch(ctx, node, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update labels and taints on node %s: %w", node.Name, err)
	}
	return nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) deletePlaceholderDeployment(ctx context.Context, hc *hyperv1.HostedCluster) error {
	deployment := placeholderDeployment(hc)
	_, err := util.DeleteIfNeeded(ctx, r, deployment)
	return err
}

func (r *DedicatedServingComponentSchedulerAndSizer) takenNodePairLabels(ctx context.Context) ([]string, error) {
	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes, client.HasLabels{hyperv1.HostedClusterLabel, OSDFleetManagerPairedNodesLabel}); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	result := sets.New[string]()
	for _, node := range nodes.Items {
		labelValue := node.Labels[OSDFleetManagerPairedNodesLabel]
		result.Insert(labelValue)
	}
	return sets.List(result), nil
}

func (r *DedicatedServingComponentSchedulerAndSizer) ensurePlaceholderDeployment(ctx context.Context, hc *hyperv1.HostedCluster, size, pairLabel string, nodesNeeded int) (*appsv1.Deployment, error) {
	deployment := placeholderDeployment(hc)
	nodeSelector := map[string]string{
		hyperv1.RequestServingComponentLabel: "true",
		hyperv1.NodeSizeLabel:                size,
	}
	var nodeAffinity *corev1.NodeAffinity
	var podAffinity *corev1.PodAffinity

	if deployment.Labels == nil {
		deployment.Labels = map[string]string{}
	}
	deployment.Labels[HostedClusterNameLabel] = hc.Name
	deployment.Labels[HostedClusterNamespaceLabel] = hc.Namespace

	if pairLabel != "" {
		nodeSelector[OSDFleetManagerPairedNodesLabel] = pairLabel
	} else {
		unavailableNodePairs, err := r.takenNodePairLabels(ctx)
		if err != nil {
			return nil, err
		}
		podAffinity = &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							PlaceholderLabel: deployment.Name,
						},
					},
					TopologyKey: OSDFleetManagerPairedNodesLabel,
				},
			},
		}
		if len(unavailableNodePairs) > 0 {
			nodeAffinity = &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      OSDFleetManagerPairedNodesLabel,
									Operator: corev1.NodeSelectorOpNotIn,
									Values:   unavailableNodePairs,
								},
							},
						},
					},
				},
			}
		}
	}

	podAntiAffinity := &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
			{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						PlaceholderLabel: deployment.Name,
					},
				},
				TopologyKey: "topology.kubernetes.io/zone",
			},
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      PlaceholderLabel,
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
				TopologyKey: "kubernetes.io/hostname",
			},
			{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      PlaceholderLabel,
							Operator: metav1.LabelSelectorOpNotIn,
							Values:   []string{deployment.Name},
						},
					},
				},
				TopologyKey: OSDFleetManagerPairedNodesLabel,
			},
		},
	}
	desiredSpec := appsv1.DeploymentSpec{
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Replicas: ptr.To(int32(nodesNeeded)),
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				PlaceholderLabel: deployment.Name,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					PlaceholderLabel: deployment.Name,
				},
			},
			Spec: corev1.PodSpec{
				Affinity: &corev1.Affinity{
					NodeAffinity:    nodeAffinity,
					PodAffinity:     podAffinity,
					PodAntiAffinity: podAntiAffinity,
				},
				NodeSelector: nodeSelector,
				Tolerations: []corev1.Toleration{
					{
						Key:      ControlPlaneServingComponentTaint,
						Effect:   corev1.TaintEffectNoSchedule,
						Operator: corev1.TolerationOpEqual,
						Value:    "true",
					},
					{
						Key:      ControlPlaneTaint,
						Effect:   corev1.TaintEffectNoSchedule,
						Operator: corev1.TolerationOpEqual,
						Value:    "true",
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "placeholder",
						Image: defaultPlaceholderImage,
					},
				},
			},
		},
	}
	if _, err := r.createOrUpdate(ctx, r, deployment, func() error {
		deployment.Spec = desiredSpec
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to ensure placeholder deployment: %w", err)
	}
	return deployment, nil
}

func placeholderDeployment(hc *hyperv1.HostedCluster) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterKey(hc),
			Namespace: autoSizerNamespace,
		},
	}
}

func clusterKey(hc *hyperv1.HostedCluster) string {
	return fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
}
