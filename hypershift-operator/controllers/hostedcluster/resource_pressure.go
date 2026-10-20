package hostedcluster

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxResourcePressureMessageLen = 256

// insufficientResourcePattern matches scheduler messages like "Insufficient cpu",
// "Insufficient memory", "Insufficient ephemeral-storage", etc. Only the resource
// type name is captured — node names, counts, and other scheduler internals are
// excluded to prevent leaking management cluster topology.
// The resource name is restricted to word characters and hyphens to avoid capturing
// trailing punctuation (commas, periods) from the scheduler message format.
var insufficientResourcePattern = regexp.MustCompile(`Insufficient\s+([\w][\w-]*)`)

// reconcileManagementClusterResourcePressure detects scheduling pressure in the
// control plane namespace by reading ControlPlaneComponent conditions that the
// CPO has already diagnosed. This avoids listing Pods or Events from the
// hypershift-operator, which would either create cluster-wide informers (cached
// client) or add per-reconcile API server load (uncached client).
//
// The CPO's diagnosePodFailure already inspects pods per-component and sets
// ControlPlaneComponentAvailable with Reason=PodSchedulingFailed and a sanitized
// message containing only resource types (no node names or topology).
//
// The resulting message lists affected components and resource types (e.g.,
// "2 components with scheduling failures (etcd, kube-apiserver): Insufficient cpu,
// Insufficient memory") rather than listing pod names, to avoid exposing
// management cluster topology.
//
// ObservedGeneration is not set because this condition is event-driven (derived
// from component status), not spec-driven.
func reconcileManagementClusterResourcePressure(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) {
	componentsList := &hyperv1.ControlPlaneComponentList{}
	if err := c.List(ctx, componentsList, client.InNamespace(controlPlaneNamespace)); err != nil {
		meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ManagementClusterResourcePressure),
			Status:  metav1.ConditionUnknown,
			Reason:  hyperv1.ReasonControlPlanePodStatusUnavailable,
			Message: "Unable to determine scheduling pressure: control plane component status could not be retrieved",
		})
		return
	}

	// No components yet — nothing to evaluate.
	if len(componentsList.Items) == 0 {
		meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ManagementClusterResourcePressure),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReasonNoPressure,
			Message: "No scheduling failures detected",
		})
		return
	}

	var affectedComponents []string
	resourceTypes := make(map[string]struct{})

	for i := range componentsList.Items {
		cpc := &componentsList.Items[i]
		avail := meta.FindStatusCondition(cpc.Status.Conditions, string(hyperv1.ControlPlaneComponentAvailable))
		if avail == nil || avail.Reason != hyperv1.ReasonPodSchedulingFailed {
			continue
		}
		affectedComponents = append(affectedComponents, cpc.Name)
		// Extract resource types from the already-sanitized CPC message.
		extractResourceTypes(avail.Message, resourceTypes)
	}

	if len(affectedComponents) == 0 {
		meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ManagementClusterResourcePressure),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReasonNoPressure,
			Message: "No scheduling failures detected",
		})
		return
	}

	sort.Strings(affectedComponents)
	msg := buildResourcePressureMessage(affectedComponents, resourceTypes)
	meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.ManagementClusterResourcePressure),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.ReasonFailedScheduling,
		Message: msg,
	})
}

// extractResourceTypes parses a message for "Insufficient <resource>"
// patterns and records each unique resource type found. Only the
// resource type is extracted — node names, counts, and other internals are
// excluded.
func extractResourceTypes(message string, types map[string]struct{}) {
	matches := insufficientResourcePattern.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		types["Insufficient "+m[1]] = struct{}{}
	}
}

// buildResourcePressureMessage constructs the condition message:
// "2 components with scheduling failures (etcd, kube-apiserver): Insufficient cpu, Insufficient memory"
// If no resource types were extracted, falls back to a component-list-only message.
func buildResourcePressureMessage(components []string, resourceTypes map[string]struct{}) string {
	noun := "components"
	if len(components) == 1 {
		noun = "component"
	}

	msg := fmt.Sprintf("%d %s with scheduling failures (%s)", len(components), noun, strings.Join(components, ", "))

	if len(resourceTypes) > 0 {
		sorted := make([]string, 0, len(resourceTypes))
		for name := range resourceTypes {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		msg += ": " + strings.Join(sorted, ", ")
	}

	if len(msg) > maxResourcePressureMessageLen {
		msg = msg[:maxResourcePressureMessageLen-3] + "..."
	}
	return msg
}
