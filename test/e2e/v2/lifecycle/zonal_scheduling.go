//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

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
// contract. The returned cleanup function restores each modified label to its prior state
// (re-setting the previous value, or removing the label if it was absent) and returns an
// error if any restore fails; it is safe to call even if some nodes were deleted in the
// meantime.
//
// The labels are additive and namespaced under hypershift.openshift.io; no other
// workload selects on them, so labeling is non-disruptive to other hosted clusters on
// the same management cluster.
func LabelManagementNodesForZonalScheduling(ctx context.Context, cl crclient.Client) (cleanup func() error, err error) {
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

	var changes []nodeLabelState
	for name, role := range roleByNode {
		node := &corev1.Node{}
		if err := cl.Get(ctx, crclient.ObjectKey{Name: name}, node); err != nil {
			return makeCleanup(cl, changes), fmt.Errorf("getting node %s: %w", name, err)
		}
		priorValue, existed := node.Labels[hyperv1.ControlPlaneNodeRoleLabel]
		if existed && priorValue == role {
			continue
		}
		original := node.DeepCopy()
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[hyperv1.ControlPlaneNodeRoleLabel] = role
		if err := cl.Patch(ctx, node, crclient.MergeFrom(original)); err != nil {
			return makeCleanup(cl, changes), fmt.Errorf("labeling node %s as %s: %w", name, role, err)
		}
		changes = append(changes, nodeLabelState{name: name, existed: existed, priorValue: priorValue})
	}

	return makeCleanup(cl, changes), nil
}

// nodeLabelState records the prior state of the control-plane-node-role label on a node
// that LabelManagementNodesForZonalScheduling modified, so cleanup can restore it.
type nodeLabelState struct {
	name       string
	existed    bool
	priorValue string
}

// makeCleanup returns a function that restores hyperv1.ControlPlaneNodeRoleLabel on each
// modified node to its prior state (re-setting the previous value, or removing the label
// if it was absent). It ignores nodes that no longer exist, stops on the first
// non-NotFound Get error, and propagates Patch errors.
func makeCleanup(cl crclient.Client, changes []nodeLabelState) func() error {
	return func() error {
		for _, change := range changes {
			node := &corev1.Node{}
			if err := cl.Get(context.Background(), crclient.ObjectKey{Name: change.name}, node); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return fmt.Errorf("getting node %s for cleanup: %w", change.name, err)
			}
			original := node.DeepCopy()
			if change.existed {
				if node.Labels == nil {
					node.Labels = map[string]string{}
				}
				node.Labels[hyperv1.ControlPlaneNodeRoleLabel] = change.priorValue
			} else {
				delete(node.Labels, hyperv1.ControlPlaneNodeRoleLabel)
			}
			if err := cl.Patch(context.Background(), node, crclient.MergeFrom(original)); err != nil {
				return fmt.Errorf("restoring node %s label for cleanup: %w", change.name, err)
			}
		}
		return nil
	}
}
