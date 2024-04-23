package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	controllerName            = "RequestServingNodeAutoscaler"
	machineSetAnnotation      = "hypershift.openshift.io/machineset"
	machineSetNamespace       = "openshift-machine-api"
	machineNameNodeAnnotation = "machine.openshift.io/machine"
	machineMachineSetLabel    = "machine.openshift.io/cluster-api-machineset"
)

type RequestServingNodeAutoscaler struct {
	client.Client
}

func (r *RequestServingNodeAutoscaler) SetupWithManager(mgr ctrl.Manager) error {
	r.Client = mgr.GetClient()
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates()).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
			MaxConcurrentReconciles: 1,
		}).Named(controllerName)
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

	machineSetsToScale := determineMachineSetsToScale(podList.Items, machineSets, machineList.Items, nodeList.Items)

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

func determineMachineSetsToScale(pods []corev1.Pod, machineSets []machinev1beta1.MachineSet, machines []machinev1beta1.Machine, nodes []corev1.Node) []machinev1beta1.MachineSet {
	var result []machinev1beta1.MachineSet
	pendingPods := filterPods(pods, isPodPending)
	if len(pendingPods) == 0 {
		return nil
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
		return result
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

	return result
}

type nodeRequirement struct {
	sizeLabel string
	pairLabel string
	count     int
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

func determineRequiredNodes(pendingPods, allPods []corev1.Pod, nodes []corev1.Node) []nodeRequirement {
	var result []nodeRequirement
	skipPods := sets.New[string]()
	for i := range pendingPods {
		pod := &pendingPods[i]
		if skipPods.Has(pod.Name) {
			continue
		}
		if pairLabel := podPairLabel(pod, nodes); pairLabel != "" {
			addRequirement(&result, podSize(pod), pairLabel, 1)
			continue
		}
		pairPod := getPairPod(pod, pendingPods)
		if pairPod != nil {
			addRequirement(&result, podSize(pod), "", 2)
			skipPods.Insert(pairPod.Name)
			continue
		}
		pairPod = getPairPod(pod, allPods)
		if pairPod != nil {
			addRequirement(&result, podSize(pod), podPairLabel(pairPod, nodes), 1)
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
	machineName := parts[1]
	for _, m := range machines {
		if m.Name == machineName {
			return m.Labels[machineMachineSetLabel]
		}
	}
	return ""
}

func isPodPending(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodPending
}
