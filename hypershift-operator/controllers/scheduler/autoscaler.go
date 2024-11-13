package scheduler

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/support/util"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	autoscalerControllerName                  = "RequestServingNodeAutoscaler"
	descalerControllerName                    = "MachineSetDescaler"
	nonRequestServingAutoscalerControllerName = "NonRequestServingNodeAutoscaler"
	machineSetAnnotation                      = "hypershift.openshift.io/machineset"
	machineSetNamespace                       = "openshift-machine-api"
	machineNameNodeAnnotation                 = "machine.openshift.io/machine"
	machineMachineSetLabel                    = "machine.openshift.io/cluster-api-machineset"
	clusterAPIMachineTypeLabel                = "machine.openshift.io/cluster-api-machine-type"
	nonRequestServingLabelPrefix              = "non-serving"
	maxSizeMachineSetAnnotation               = "machine.openshift.io/cluster-api-autoscaler-node-group-max-size"
	minSizeMachineSetAnnotation               = "machine.openshift.io/cluster-api-autoscaler-node-group-min-size"
)

const (
	// nodeScaleDownDelay is the amount of time a node must exist before it is considered for scaling down
	nodeScaleDownDelay = 5 * time.Minute
)

type NonRequestServingNodeAutoscaler struct {
	client.Client
}

func (r *NonRequestServingNodeAutoscaler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedCluster{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Watches(&machinev1beta1.MachineSet{}, &handler.EnqueueRequestForObject{}).
		Watches(&schedulingv1alpha1.ClusterSizingConfiguration{}, &handler.EnqueueRequestForObject{}).
		Named(nonRequestServingAutoscalerControllerName)
	return builder.Complete(r)
}

func (r *NonRequestServingNodeAutoscaler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	config := &schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, config); err != nil {
		return ctrl.Result{}, fmt.Errorf("could not get cluster sizing configuration: %w", err)
	}
	if err := validateConfigForNonRequestServing(config); err != nil {
		log.Info("Invalid cluster sizing configuration, skipping for now", "msg", err)
		return ctrl.Result{}, nil
	}

	hostedClusters := &hyperv1.HostedClusterList{}
	if err := r.List(ctx, hostedClusters); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list hosted clusters: %w", err)
	}

	machineSets := &machinev1beta1.MachineSetList{}
	if err := r.List(ctx, machineSets, client.InNamespace(machineSetNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list machinesets: %w", err)
	}

	nonReqServingMachineSets := filterMachineSets(machineSets.Items, func(ms *machinev1beta1.MachineSet) bool {
		return strings.HasPrefix(ms.Spec.Template.ObjectMeta.Labels[clusterAPIMachineTypeLabel], nonRequestServingLabelPrefix)
	})

	if err := validateNonRequestServingMachineSets(nonReqServingMachineSets); err != nil {
		log.Info("Invalid non request serving machinesets", "error", err)
		return ctrl.Result{}, nil
	}

	machineSetsWithReplicas := nonRequestServingMachineSetsToScale(ctx, config, hostedClusters.Items, nonReqServingMachineSets)
	var errs []error
	for _, msr := range machineSetsWithReplicas {
		log.Info("Scaling non request serving machineset", "machineset", msr.machineSet.Name, "replicas", msr.replicas)
		scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: msr.replicas}}
		if err := r.SubResource("scale").Update(ctx, &msr.machineSet, client.WithSubResourceBody(scale)); err != nil {
			errs = append(errs, fmt.Errorf("failed to scale non request serving machineset %s: %w", msr.machineSet.Name, err))
		}
	}
	return ctrl.Result{}, utilerrors.NewAggregate(errs)
}

type MachineSetDescaler struct {
	client.Client
}

func (r *MachineSetDescaler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Watches(&hyperv1.HostedCluster{}, handler.EnqueueRequestsFromMapFunc(mapHostedClusterToNodesFn(r.Client, mgr))).
		Named(descalerControllerName)
	return builder.Complete(r)
}

func (r *MachineSetDescaler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("node", req.Name)
	node := &corev1.Node{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get node: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if _, hasServingComponentLabel := node.Labels[hyperv1.RequestServingComponentLabel]; !hasServingComponentLabel {
		return ctrl.Result{}, nil
	}

	if _, hasHostedClusterLabel := node.Labels[hyperv1.HostedClusterLabel]; !hasHostedClusterLabel {
		return ctrl.Result{}, nil
	}

	hcName := node.Labels[HostedClusterNameLabel]
	hcNamespace := node.Labels[HostedClusterNamespaceLabel]
	if hcName == "" || hcNamespace == "" {
		return ctrl.Result{}, nil
	}

	hostedCluster := &hyperv1.HostedCluster{}
	hcNotFound := false
	if err := r.Get(ctx, types.NamespacedName{Name: hcName, Namespace: hcNamespace}, hostedCluster); err != nil {
		if errors.IsNotFound(err) {
			hcNotFound = true
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster: %w", err)
		}
	}
	machineSetList := &machinev1beta1.MachineSetList{}
	if err := r.List(ctx, machineSetList, client.InNamespace(machineSetNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list machinesets: %w", err)
	}
	machineList := &machinev1beta1.MachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(machineSetNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list machines: %w", err)
	}
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}
	var toScaleDown []machinev1beta1.MachineSet
	var requeueAfter time.Duration
	if hcNotFound {
		toScaleDown = nodeMachineSetsToScaleDown(node, machineSetList.Items, machineList.Items, nodeList.Items)
	} else {
		toScaleDown, requeueAfter = hostedClusterMachineSetsToScaleDown(ctx, hostedCluster, machineSetList.Items, machineList.Items, nodeList.Items)
	}

	if len(toScaleDown) == 0 {
		if requeueAfter > 0 {
			log.Info("Requeuing reconciliation", "after", requeueAfter)
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	var errs []error
	for i := range toScaleDown {
		scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 0}}
		log.Info("Scaling down machineset", "machineset", toScaleDown[i].Name)
		if err := r.SubResource("scale").Update(ctx, &toScaleDown[i], client.WithSubResourceBody(scale)); err != nil {
			errs = append(errs, fmt.Errorf("failed to scale down machineset %s: %w", toScaleDown[i].Name, err))
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, utilerrors.NewAggregate(errs)
}

// nodeMachineSetsToScaleDown returns a list of machine sets that should be scaled down
// given a node labeled for a HostedCluster that no longer exists
func nodeMachineSetsToScaleDown(node *corev1.Node, machineSets []machinev1beta1.MachineSet, machines []machinev1beta1.Machine, nodes []corev1.Node) []machinev1beta1.MachineSet {
	var nodesToScaleDown []corev1.Node
	pairLabel := node.Labels[OSDFleetManagerPairedNodesLabel]
	if pairLabel != "" {
		nodesToScaleDown = filterNodes(nodes, func(n *corev1.Node) bool {
			return n.Labels[OSDFleetManagerPairedNodesLabel] == pairLabel && n.Labels[hyperv1.NodeSizeLabel] != ""
		})
	} else {
		if node.Labels[hyperv1.NodeSizeLabel] != "" {
			nodesToScaleDown = []corev1.Node{*node}
		}
	}
	var result []machinev1beta1.MachineSet
	for _, n := range nodesToScaleDown {
		msName := nodeMachineSet(&n, machines)
		ms := findMachineSet(msName, machineSets)
		if ms != nil {
			if ptr.Deref(ms.Spec.Replicas, 0) == 0 {
				continue
			}
			result = append(result, *ms)
		}
	}
	return result
}

// hostedClusterMachineSetsToScaleDown returns a list of machine sets that should be scaled down
// given the current state of a HostedCluster
func hostedClusterMachineSetsToScaleDown(ctx context.Context, hostedCluster *hyperv1.HostedCluster, machineSets []machinev1beta1.MachineSet, machines []machinev1beta1.Machine, nodes []corev1.Node) ([]machinev1beta1.MachineSet, time.Duration) {
	var result []machinev1beta1.MachineSet
	log := ctrl.LoggerFrom(ctx)

	additionalNodeSelector := util.ParseNodeSelector(hostedCluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation])
	var sizeLabelSelector map[string]string
	if sizeLabel := hostedCluster.Labels[hyperv1.HostedClusterSizeLabel]; sizeLabel != "" {
		sizeLabelSelector = map[string]string{hyperv1.NodeSizeLabel: sizeLabel}
	}
	if len(additionalNodeSelector) == 0 && len(sizeLabelSelector) == 0 {
		return nil, 0
	}
	var clusterNodes []corev1.Node
	nodesWithClusterLabel := filterNodes(nodes, func(n *corev1.Node) bool {
		return n.Labels[hyperv1.HostedClusterLabel] == clusterKey(hostedCluster)
	})
	if len(nodesWithClusterLabel) > 0 {
		pairLabel := nodesWithClusterLabel[0].Labels[OSDFleetManagerPairedNodesLabel]
		clusterNodes = filterNodes(nodes, func(n *corev1.Node) bool {
			return n.Labels[OSDFleetManagerPairedNodesLabel] == pairLabel && n.Labels[hyperv1.NodeSizeLabel] != ""
		})
	}

	activeNodes := filterNodes(clusterNodes, func(n *corev1.Node) bool {
		return labelsMatchSelector(n.Labels, sizeLabelSelector) || labelsMatchSelector(n.Labels, additionalNodeSelector)
	})
	if len(activeNodes) == 0 {
		return nil, 0
	}
	nodesToScaleDown := filterNodes(clusterNodes, func(n *corev1.Node) bool {
		return findNode(n.Name, activeNodes) == nil
	})
	if len(nodesToScaleDown) == 0 {
		return nil, 0
	}
	log.Info("Nodes to scale down", "toScaleDown", nodeNames(nodesToScaleDown), "clusterNodes", nodeNames(clusterNodes), "activeNodes", nodeNames(activeNodes))
	newerNodes := 0
	for _, node := range nodesToScaleDown {
		// Skip nodes that are too new to scale down
		if node.CreationTimestamp.Time.Add(nodeScaleDownDelay).After(time.Now()) {
			newerNodes++
			continue
		}
		msName := nodeMachineSet(&node, machines)
		ms := findMachineSet(msName, machineSets)
		if ms != nil {
			if ms.Spec.Replicas == nil || *ms.Spec.Replicas == 0 {
				continue
			}
			result = append(result, *ms)
		}
	}
	var requeue time.Duration
	if newerNodes > 0 {
		log.Info("Newer nodes exist, requeueing reconciliation", "count", newerNodes)
		requeue = nodeScaleDownDelay
	}
	return result, requeue
}

type RequestServingNodeAutoscaler struct {
	client.Client
}

func (r *RequestServingNodeAutoscaler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	tickerChannel := make(chan event.GenericEvent)
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WatchesRawSource(source.Channel(tickerChannel, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).Named(autoscalerControllerName)

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ticker", Namespace: placeholderNamespace}}
			tickerChannel <- event.GenericEvent{Object: pod}
		}
	}()
	return builder.Complete(r)
}

type machineSetsByName []machinev1beta1.MachineSet

func (m machineSetsByName) Len() int {
	return len(m)
}

func (m machineSetsByName) Less(i, j int) bool {
	return m[i].Name < m[j].Name
}

func (m machineSetsByName) Swap(i, j int) {
	ms := m[i]
	m[i] = m[j]
	m[j] = ms
}

func (r *RequestServingNodeAutoscaler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace != placeholderNamespace {
		return ctrl.Result{}, nil
	}
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(placeholderNamespace), client.HasLabels{PlaceholderLabel}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list placeholder pods: %w", err)
	}
	machineSetList := &machinev1beta1.MachineSetList{}
	if err := r.List(ctx, machineSetList, client.InNamespace(machineSetNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list machinesets: %w", err)
	}
	machineSets := machineSetList.Items
	// Sort machinesets to get a deterministic result
	sort.Sort(machineSetsByName(machineSets))
	machineList := &machinev1beta1.MachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(machineSetNamespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list machines: %w", err)
	}
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list nodes: %w", err)
	}

	machineSetsToScale, pendingPods, requiredNodes := machineSetsToScaleUp(podList.Items, machineSets, machineList.Items, nodeList.Items)
	if len(machineSetsToScale) > 0 {
		log.Info("Machinesets to scale", "machinesets", machineSetNames(machineSetsToScale),
			"pending pods", podNames(pendingPods), "required nodes", requiredNodes)
	}

	var errs []error
	for i := range machineSetsToScale {
		scale := &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 1}}
		log.Info("Scaling up machineset", "machineset", machineSetsToScale[i].Name)
		if err := r.SubResource("scale").Update(ctx, &machineSetsToScale[i], client.WithSubResourceBody(scale)); err != nil {
			errs = append(errs, fmt.Errorf("failed to scale up machineset %s: %w", machineSetsToScale[i].Name, err))
		}
	}
	return ctrl.Result{}, utilerrors.NewAggregate(errs)
}

func machineSetsToScaleUp(pods []corev1.Pod, machineSets []machinev1beta1.MachineSet, machines []machinev1beta1.Machine, nodes []corev1.Node) ([]machinev1beta1.MachineSet, []corev1.Pod, []nodeRequirement) {
	var result []machinev1beta1.MachineSet
	pendingPods := filterPods(pods, isPodPending)
	if len(pendingPods) == 0 {
		return nil, nil, nil
	}
	requiredNodeCounts := determineRequiredNodes(pendingPods, pods, nodes)

	var placeHoldersNeeded []nodeRequirement

	// First, the easy ones. If a specific pair label is required,
	// find the corresponding machinesets that are not already
	// scaled up.
	for _, r := range requiredNodeCounts {
		if r.pairLabel != "" {
			machineSetsToScale := filterMachineSets(machineSets, func(ms *machinev1beta1.MachineSet) bool {
				return machineSetPairLabel(ms) == r.pairLabel &&
					machineSetSize(ms) == r.sizeLabel &&
					ptr.Deref(ms.Spec.Replicas, 0) == 0
			})
			result = append(result, machineSetsToScale...)
			continue
		}

		// Otherwise, we need to find placeholders without a specific pair label
		placeHoldersNeeded = append(placeHoldersNeeded, r)
	}

	if len(placeHoldersNeeded) == 0 {
		return result, pendingPods, requiredNodeCounts
	}

	// Determine which pair labels we cannot
	// use to schedule additional placeholders
	// 1 - pair labels used by a cluster
	// 2 - pair labels where a placeholder is already scheduled
	takenPairLabels := sets.New[string]()
	for _, n := range nodes {
		if n.Labels[hyperv1.HostedClusterLabel] != "" {
			takenPairLabels.Insert(n.Labels[OSDFleetManagerPairedNodesLabel])
		}
	}
	for _, p := range pods {
		if pairLabel := podPairLabel(&p, nodes); pairLabel != "" {
			takenPairLabels.Insert(pairLabel)
		}
	}

	for _, r := range placeHoldersNeeded {
		needCount := r.count
		// First, find any available nodes of the specified size
		// These are nodes that are created but may not be ready
		// but will allow scheduling of the pods soon.
		// Available nodes must:
		// 1 - have the request serving label
		// 2 - have matching size label
		// 3 - have a pair label that is not already taken
		availableNodes := filterNodes(nodes, func(n *corev1.Node) bool {
			return n.Labels[hyperv1.RequestServingComponentLabel] != "" &&
				n.Labels[hyperv1.NodeSizeLabel] == r.sizeLabel &&
				!takenPairLabels.Has(n.Labels[OSDFleetManagerPairedNodesLabel])
		})
		needCount -= len(availableNodes)

		availableNodeMachineSets := sets.New[string]()
		for i := range availableNodes {
			msName := nodeMachineSet(&availableNodes[i], machines)
			if msName == "" {
				continue
			}
			availableNodeMachineSets.Insert(msName)
		}

		// Second, find any machinesets that have already been scaled up
		// but do not have any nodes yet.
		// Pending machinesets must:
		// 1 - have the request serving label
		// 2 - have matching size label
		// 3 - be scaled up without available replicas
		// 4 - not correspond to any available nodes
		// 5 - not have a pair label that is assigned to a cluster
		pendingMachineSets := filterMachineSets(machineSets, func(ms *machinev1beta1.MachineSet) bool {
			return isRequestServingMachineSet(ms) &&
				machineSetSize(ms) == r.sizeLabel &&
				ptr.Deref(ms.Spec.Replicas, 0) > 0 &&
				ms.Status.AvailableReplicas == 0 &&
				!availableNodeMachineSets.Has(ms.Name) &&
				!takenPairLabels.Has(machineSetPairLabel(ms))
		})
		needCount -= len(pendingMachineSets)

		if needCount < 1 {
			continue
		}

		// Determine if there are pending machinesets that need the machineSet pair to also be scaled up
		// and scale those up first
		for _, ms := range pendingMachineSets {
			if pairedMachineSet := matchingMachineSet(&ms, machineSets); pairedMachineSet != nil {
				if ptr.Deref(pairedMachineSet.Spec.Replicas, 0) == 0 {
					result = append(result, *pairedMachineSet)
					needCount--
				}
			}
		}

		if needCount < 1 {
			continue
		}

		// Finally, pick random pairs from available machinesets
		// Available machinesets must:
		// 1 - have the request serving label
		// 2 - have the corresponding size label
		// 3 - not be scaled up
		// 4 - have a pair label that is not already taken
		availableMachineSets := filterMachineSets(machineSets, func(ms *machinev1beta1.MachineSet) bool {
			return isRequestServingMachineSet(ms) &&
				machineSetSize(ms) == r.sizeLabel &&
				ptr.Deref(ms.Spec.Replicas, 0) == 0 &&
				!takenPairLabels.Has(machineSetPairLabel(ms))
		})
		var machineSetsToScaleUp []machinev1beta1.MachineSet
		toSkip := sets.New[string]()
		for _, ms := range availableMachineSets {
			if toSkip.Has(ms.Name) {
				continue
			}
			pairMachineSet := matchingMachineSet(&ms, availableMachineSets)
			if pairMachineSet == nil {
				continue
			}
			toSkip.Insert(pairMachineSet.Name)
			machineSetsToScaleUp = append(machineSetsToScaleUp, ms, *pairMachineSet)
			if len(machineSetsToScaleUp) >= needCount {
				break
			}
		}
		result = append(result, machineSetsToScaleUp...)
	}

	return result, pendingPods, requiredNodeCounts
}

type nodeRequirement struct {
	sizeLabel string
	pairLabel string
	count     int
}

func (n nodeRequirement) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("{%q: %q, %q: %q, %q: %d}", "size", n.sizeLabel, "pair", n.pairLabel, "count", n.count)), nil
}

func addRequirement(reqs *[]nodeRequirement, sizeLabel, pairLabel string, count int) {
	found := false
	for i := range *reqs {
		req := (*reqs)[i]
		if req.sizeLabel == sizeLabel && req.pairLabel == pairLabel {
			req.count += count
			(*reqs)[i] = req
			found = true
			break
		}
	}
	if !found {
		*reqs = append(*reqs, nodeRequirement{
			sizeLabel: sizeLabel,
			pairLabel: pairLabel,
			count:     count,
		})
	}
}

type podPair struct {
	p1 corev1.Pod
	p2 corev1.Pod
}

func findPodPairs(pendingPods, allPods []corev1.Pod) ([]podPair, []corev1.Pod) {
	var result []podPair
	var singlePods []corev1.Pod
	skipPods := sets.New[string]()
	for i := range pendingPods {
		pod := &pendingPods[i]
		if skipPods.Has(pod.Name) {
			continue
		}
		pairPod := getPairPod(pod, allPods)
		if pairPod != nil {
			result = append(result, podPair{p1: *pod, p2: *pairPod})
			skipPods.Insert(pairPod.Name)
		} else {
			singlePods = append(singlePods, *pod)
		}
	}
	return result, singlePods
}

func determineRequiredNodes(pendingPods, allPods []corev1.Pod, nodes []corev1.Node) []nodeRequirement {
	var result []nodeRequirement
	podPairs, singlePods := findPodPairs(pendingPods, allPods)

	for _, pair := range podPairs {
		pairLabel := podPairLabel(&pair.p1, nodes)
		if pairLabel == "" {
			pairLabel = podPairLabel(&pair.p2, nodes)
		}
		pendingCount := 0
		for _, p := range []corev1.Pod{pair.p1, pair.p2} {
			if isPodPending(&p) {
				pendingCount++
			}
		}
		addRequirement(&result, podSize(&pair.p1), pairLabel, pendingCount)
	}
	for _, pod := range singlePods {
		pairLabel := podPairLabel(&pod, nodes)
		// Only add a requirement for pods that have a specific pair label requirement
		// These are for a specific hosted cluster. Single pods that do not have a specific
		// pair label requirement are generic placeholders and are likely in the middle of
		// being rolled out by their corresponding deployment.
		if pairLabel != "" {
			addRequirement(&result, podSize(&pod), pairLabel, 1)
		}
	}
	return result
}

func getPairPod(pod *corev1.Pod, pods []corev1.Pod) *corev1.Pod {
	for i, p := range pods {
		if p.Name == pod.Name {
			continue
		}
		if reflect.DeepEqual(pod.Labels, p.Labels) {
			return &pods[i]
		}
	}
	return nil
}

func matchingMachineSet(machineSet *machinev1beta1.MachineSet, machineSets []machinev1beta1.MachineSet) *machinev1beta1.MachineSet {
	for i := range machineSets {
		ms := &machineSets[i]
		if ms.Name == machineSet.Name {
			continue
		}
		if machineSetSize(ms) == machineSetSize(machineSet) && machineSetPairLabel(ms) == machineSetPairLabel(machineSet) {
			return ms
		}
	}
	return nil
}

func machineSetSize(machineSet *machinev1beta1.MachineSet) string {
	return machineSet.Spec.Template.Spec.ObjectMeta.Labels[hyperv1.NodeSizeLabel]
}

func machineSetPairLabel(machineSet *machinev1beta1.MachineSet) string {
	return machineSet.Spec.Template.Spec.ObjectMeta.Labels[OSDFleetManagerPairedNodesLabel]
}

func isRequestServingMachineSet(machineSet *machinev1beta1.MachineSet) bool {
	return machineSet.Spec.Template.Spec.ObjectMeta.Labels[hyperv1.RequestServingComponentLabel] == "true"
}

func podSize(pod *corev1.Pod) string {
	return pod.Spec.NodeSelector[hyperv1.NodeSizeLabel]
}

func podPairLabel(pod *corev1.Pod, nodes []corev1.Node) string {
	if pod.Spec.NodeName != "" {
		node := findNode(pod.Spec.NodeName, nodes)
		if node != nil {
			return node.Labels[OSDFleetManagerPairedNodesLabel]
		}
	}
	return pod.Spec.NodeSelector[OSDFleetManagerPairedNodesLabel]
}

func findNode(name string, nodes []corev1.Node) *corev1.Node {
	for i := range nodes {
		node := &nodes[i]
		if node.Name == name {
			return node
		}
	}
	return nil
}

func filterPods(pods []corev1.Pod, predicate func(*corev1.Pod) bool) []corev1.Pod {
	filtered := make([]corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if predicate(&pod) {
			filtered = append(filtered, pod)
		}
	}
	return filtered
}

func filterMachineSets(machineSets []machinev1beta1.MachineSet, predicate func(*machinev1beta1.MachineSet) bool) []machinev1beta1.MachineSet {
	filtered := make([]machinev1beta1.MachineSet, 0, len(machineSets))
	for _, ms := range machineSets {
		if predicate(&ms) {
			filtered = append(filtered, ms)
		}
	}
	return filtered
}

func filterNodes(nodes []corev1.Node, predicate func(*corev1.Node) bool) []corev1.Node {
	filtered := make([]corev1.Node, 0, len(nodes))
	for _, n := range nodes {
		if predicate(&n) {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

func nodeMachineSet(n *corev1.Node, machines []machinev1beta1.Machine) string {
	namespacedName := n.Annotations[machineNameNodeAnnotation]
	if namespacedName == "" {
		return ""
	}
	parts := strings.Split(namespacedName, "/")
	if len(parts) != 2 {
		return ""
	}
	machineName := parts[1]
	for _, m := range machines {
		if m.Name == machineName {
			return m.Labels[machineMachineSetLabel]
		}
	}
	return ""
}

func isPodPending(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodPending && p.DeletionTimestamp.IsZero()
}

func labelsMatchSelector(objectLabels map[string]string, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	return labels.Set(selector).AsSelector().Matches(labels.Set(objectLabels))
}

func findMachineSet(name string, machineSets []machinev1beta1.MachineSet) *machinev1beta1.MachineSet {
	for i := range machineSets {
		ms := &machineSets[i]
		if ms.Name == name {
			return ms
		}
	}
	return nil
}

func nodeNames(nodes []corev1.Node) []string {
	names := make([]string, 0, len(nodes))
	for i := range nodes {
		names = append(names, nodes[i].Name)
	}
	return names
}

func machineSetNames(machineSets []machinev1beta1.MachineSet) []string {
	names := make([]string, 0, len(machineSets))
	for i := range machineSets {
		names = append(names, machineSets[i].Name)
	}
	return names
}

func podNames(pods []corev1.Pod) []string {
	names := make([]string, 0, len(pods))
	for i := range pods {
		names = append(names, pods[i].Name)
	}
	return names
}

func validateConfigForNonRequestServing(config *schedulingv1alpha1.ClusterSizingConfiguration) error {
	if condition := meta.FindStatusCondition(config.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		msg := ""
		if condition != nil {
			msg = condition.Message
		}
		return fmt.Errorf("cluster sizing configuration is not valid: %s", msg)
	}

	for _, sizeConfig := range config.Spec.Sizes {
		if sizeConfig.Management == nil || sizeConfig.Management.NonRequestServingNodesPerZone == nil {
			return fmt.Errorf("non request serving nodes per zone is not set for size %s", sizeConfig.Name)
		}
	}
	return nil
}

func validateNonRequestServingMachineSets(machineSets []machinev1beta1.MachineSet) error {
	if len(machineSets) != 3 {
		return fmt.Errorf("expected 3 non request serving machinesets, found %d", len(machineSets))
	}

	// Ensure that we have consistent min/max size across machinesets
	minSizes := sets.New[int]()
	maxSizes := sets.New[int]()
	for _, ms := range machineSets {
		minSize, err := strconv.Atoi(ms.Annotations[minSizeMachineSetAnnotation])
		if err != nil {
			return fmt.Errorf("failed to parse min size annotation (%q): %w", ms.Annotations[minSizeMachineSetAnnotation], err)
		}
		minSizes.Insert(minSize)
		maxSize, err := strconv.Atoi(ms.Annotations[maxSizeMachineSetAnnotation])
		if err != nil {
			return fmt.Errorf("failed to parse max size annotation (%q): %w", ms.Annotations[maxSizeMachineSetAnnotation], err)
		}
		maxSizes.Insert(maxSize)
	}
	if minSizes.Len() != 1 || maxSizes.Len() != 1 {
		return fmt.Errorf("inconsistent min/max sizes across non request serving machinesets")
	}

	minSize := minSizes.UnsortedList()[0]
	maxSize := maxSizes.UnsortedList()[0]

	if maxSize < minSize || maxSize < 1 {
		return fmt.Errorf("invalid min/max sizes, minSize: %d, maxSize: %d", minSize, maxSize)
	}
	return nil
}

type machineSetReplicas struct {
	machineSet machinev1beta1.MachineSet
	replicas   int32
}

func nonRequestServingMachineSetsToScale(ctx context.Context, config *schedulingv1alpha1.ClusterSizingConfiguration, hostedClusters []hyperv1.HostedCluster, machineSets []machinev1beta1.MachineSet) []machineSetReplicas {
	log := ctrl.LoggerFrom(ctx)
	hcCountBySize := make(map[string]int)
	notLabeled := 0
	for _, hc := range hostedClusters {
		sizeLabelValue := hc.Labels[hyperv1.HostedClusterSizeLabel]
		if sizeLabelValue == "" {
			notLabeled++
			continue
		}
		hcCountBySize[sizeLabelValue]++
	}
	if notLabeled > 0 {
		log.Info("WARNING: Hosted clusters without size label. Using the smallest size for them.", "count", notLabeled)
		for _, sizeConfig := range config.Spec.Sizes {
			if sizeConfig.Criteria.From == 0 {
				hcCountBySize[sizeConfig.Name] += notLabeled
				break
			}
		}
	}

	// calculate the number of non request serving nodes required per zone
	nodesNeededQty := resource.MustParse("0")
	for size, count := range hcCountBySize {
		sizeConfigFound := false
		for _, sizeConfig := range config.Spec.Sizes {
			if sizeConfig.Name == size {
				sizeConfigFound = true
				nodesForSize := *sizeConfig.Management.NonRequestServingNodesPerZone
				nodesForSize.Mul(int64(count))
				nodesNeededQty.Add(nodesForSize)
				break
			}
		}
		if !sizeConfigFound {
			log.Info("WARNING: No size configuration found for hosted cluster size", "size", size)
		}
	}
	if config.Spec.NonRequestServingNodesBufferPerZone != nil {
		nodesNeededQty.Add(*config.Spec.NonRequestServingNodesBufferPerZone)
	}
	nodesNeeded := int32(math.Ceil(nodesNeededQty.AsApproximateFloat64()))
	minNodes, _ := strconv.Atoi(machineSets[0].Annotations[minSizeMachineSetAnnotation]) // this has been validated, skipping error check
	maxNodes, _ := strconv.Atoi(machineSets[0].Annotations[maxSizeMachineSetAnnotation])

	if nodesNeeded < int32(minNodes) {
		nodesNeeded = int32(minNodes)
	}
	if nodesNeeded > int32(maxNodes) {
		nodesNeeded = int32(maxNodes)
	}

	var result []machineSetReplicas
	for _, ms := range machineSets {
		if ptr.Deref(ms.Spec.Replicas, 0) == nodesNeeded {
			continue
		}
		result = append(result, machineSetReplicas{machineSet: ms, replicas: nodesNeeded})
	}
	return result
}
