//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// minimalZonalRequiredZones is the number of distinct availability zones the
// management cluster must expose for the Minimal control plane availability-zone
// scheduling policy (etcd requires three zones for quorum spread).
const minimalZonalRequiredZones = 3

// LabelManagementNodesForZonalScheduling establishes the node contract required by
// the Minimal control plane availability-zone scheduling policy on an existing
// management cluster, without provisioning any new node pools. It labels existing,
// schedulable nodes with hyperv1.ControlPlaneNodeRoleLabel: it reserves one node as
// "overflow" and labels the remaining nodes (which must span at least three distinct
// availability zones) as "zonal".
//
// It returns an error if fewer than three distinct availability zones are present among
// schedulable nodes, so callers can Skip on management clusters that cannot satisfy the
// contract. The returned cleanup function removes the labels this call added; it is safe
// to call even if some nodes were deleted in the meantime.
//
// The labels are additive and namespaced under hypershift.openshift.io; no other
// workload selects on them, so labeling is non-disruptive to other hosted clusters on
// the same management cluster.
func LabelManagementNodesForZonalScheduling(ctx context.Context, cl crclient.Client) (cleanup func(), err error) {
	nodeList := &corev1.NodeList{}
	if err := cl.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("listing management cluster nodes: %w", err)
	}

	// Collect schedulable, role-eligible nodes grouped by availability zone. Prefer
	// nodes that are already control-plane-eligible so control plane pods keep landing
	// on the expected capacity; fall back to any schedulable node.
	byZone := map[string][]string{}
	var eligible []corev1.Node
	for i := range nodeList.Items {
		node := nodeList.Items[i]
		if node.Spec.Unschedulable {
			continue
		}
		if _, isMaster := node.Labels["node-role.kubernetes.io/master"]; isMaster {
			continue
		}
		if _, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; isControlPlane {
			continue
		}
		if node.Labels[corev1.LabelTopologyZone] == "" {
			continue
		}
		eligible = append(eligible, node)
	}
	for i := range eligible {
		zone := eligible[i].Labels[corev1.LabelTopologyZone]
		byZone[zone] = append(byZone[zone], eligible[i].Name)
	}

	if len(byZone) < minimalZonalRequiredZones {
		return nil, fmt.Errorf("management cluster exposes %d distinct availability zone(s) among schedulable nodes; the Minimal policy requires at least %d", len(byZone), minimalZonalRequiredZones)
	}

	// Deterministically choose one overflow node (from the zone with the most nodes so
	// zonal capacity across the three zones is preserved) and label the rest zonal.
	zones := make([]string, 0, len(byZone))
	for zone := range byZone {
		zones = append(zones, zone)
	}
	sort.Slice(zones, func(i, j int) bool {
		if len(byZone[zones[i]]) != len(byZone[zones[j]]) {
			return len(byZone[zones[i]]) > len(byZone[zones[j]])
		}
		return zones[i] < zones[j]
	})
	for _, names := range byZone {
		sort.Strings(names)
	}

	roleByNode := map[string]string{}
	overflowNode := byZone[zones[0]][0]
	roleByNode[overflowNode] = hyperv1.ControlPlaneNodeRoleOverflow
	for _, zone := range zones {
		for _, name := range byZone[zone] {
			if name == overflowNode {
				continue
			}
			roleByNode[name] = hyperv1.ControlPlaneNodeRoleZonal
		}
	}

	labeled := make([]string, 0, len(roleByNode))
	for name, role := range roleByNode {
		node := &corev1.Node{}
		if err := cl.Get(ctx, crclient.ObjectKey{Name: name}, node); err != nil {
			return makeCleanup(cl, labeled), fmt.Errorf("getting node %s: %w", name, err)
		}
		if node.Labels[hyperv1.ControlPlaneNodeRoleLabel] == role {
			continue
		}
		original := node.DeepCopy()
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[hyperv1.ControlPlaneNodeRoleLabel] = role
		if err := cl.Patch(ctx, node, crclient.MergeFrom(original)); err != nil {
			return makeCleanup(cl, labeled), fmt.Errorf("labeling node %s as %s: %w", name, role, err)
		}
		labeled = append(labeled, name)
	}

	return makeCleanup(cl, labeled), nil
}

// makeCleanup returns a function that removes hyperv1.ControlPlaneNodeRoleLabel from the
// named nodes, tolerating nodes that no longer exist.
func makeCleanup(cl crclient.Client, nodeNames []string) func() {
	return func() {
		for _, name := range nodeNames {
			node := &corev1.Node{}
			if err := cl.Get(context.Background(), crclient.ObjectKey{Name: name}, node); err != nil {
				continue
			}
			if _, ok := node.Labels[hyperv1.ControlPlaneNodeRoleLabel]; !ok {
				continue
			}
			original := node.DeepCopy()
			delete(node.Labels, hyperv1.ControlPlaneNodeRoleLabel)
			_ = cl.Patch(context.Background(), node, crclient.MergeFrom(original))
		}
	}
}
