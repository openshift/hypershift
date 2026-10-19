package hostedcluster

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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
// control plane namespace using two mechanisms:
//  1. FailedScheduling events on pods in the HCP namespace, correlated to the
//     involved pod by UID. An event is only counted if its involved pod still
//     exists and has PodScheduled=False (preventing stale events from keeping the
//     condition True after recovery).
//  2. Direct inspection of pods in Pending state with PodScheduled=False,
//     reason=Unschedulable (handles event TTL expiry).
//
// The resulting message groups affected pods by resource type (e.g., "3 pods with
// FailedScheduling: 2 Insufficient cpu, 1 Insufficient memory") rather than
// listing pod names, to avoid exposing management cluster topology.
//
// ObservedGeneration is not set because this condition is event-driven (derived
// from pod status and scheduling events), not spec-driven.
func reconcileManagementClusterResourcePressure(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) {
	// Step 1: List pods in the control plane namespace.
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(controlPlaneNamespace)); err != nil {
		meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ManagementClusterResourcePressure),
			Status:  metav1.ConditionUnknown,
			Reason:  hyperv1.ReasonControlPlanePodStatusUnavailable,
			Message: "Unable to determine scheduling pressure: control-plane pod status could not be retrieved",
		})
		return
	}

	// Build a UID→pod index for event correlation.
	podByUID := make(map[types.UID]*corev1.Pod, len(podList.Items))
	for i := range podList.Items {
		podByUID[podList.Items[i].UID] = &podList.Items[i]
	}

	// Track which pod UIDs have active scheduling failures, plus resource
	// type counts extracted from scheduler messages.
	affectedPodUIDs := make(map[types.UID]struct{})
	resourceTypeCounts := make(map[string]int) // e.g. "Insufficient cpu" → 2

	// Step 2: List FailedScheduling events and correlate to pods by UID.
	eventList := &corev1.EventList{}
	if err := c.List(ctx, eventList, client.InNamespace(controlPlaneNamespace),
		client.MatchingFields{"reason": "FailedScheduling"}); err != nil {
		// Field selectors on Events may not be indexed on all clusters.
		// Fall back to listing all events and filtering client-side.
		allEvents := &corev1.EventList{}
		if err2 := c.List(ctx, allEvents, client.InNamespace(controlPlaneNamespace)); err2 != nil {
			// If both fail, fall through to pod-only inspection below.
			eventList = &corev1.EventList{}
		} else {
			var filtered []corev1.Event
			for i := range allEvents.Items {
				if allEvents.Items[i].Reason == "FailedScheduling" {
					filtered = append(filtered, allEvents.Items[i])
				}
			}
			eventList = &corev1.EventList{Items: filtered}
		}
	}

	for i := range eventList.Items {
		event := &eventList.Items[i]
		if event.Reason != "FailedScheduling" {
			continue
		}

		// Correlate event to involved pod by UID.
		involvedUID := event.InvolvedObject.UID
		pod, exists := podByUID[involvedUID]
		if !exists {
			// Pod no longer exists — stale event, skip.
			continue
		}
		// Check if the pod is still unschedulable.
		if !podIsUnschedulable(pod) {
			continue
		}

		if _, seen := affectedPodUIDs[involvedUID]; !seen {
			affectedPodUIDs[involvedUID] = struct{}{}
		}

		// Extract resource types from the event message.
		extractResourceTypes(event.Message, resourceTypeCounts)
	}

	// Step 3: Direct pod status inspection — catches cases where events have
	// expired due to TTL but pods are still unschedulable.
	for i := range podList.Items {
		pod := &podList.Items[i]
		if _, alreadyCounted := affectedPodUIDs[pod.UID]; alreadyCounted {
			continue
		}
		if !podIsUnschedulable(pod) {
			continue
		}
		affectedPodUIDs[pod.UID] = struct{}{}

		// Extract resource types from the pod condition message.
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
				extractResourceTypes(cond.Message, resourceTypeCounts)
				break
			}
		}
	}

	// Step 4: Set the condition.
	if len(affectedPodUIDs) == 0 {
		meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.ManagementClusterResourcePressure),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.ReasonNoPressure,
			Message: "No scheduling failures detected",
		})
		return
	}

	msg := buildResourcePressureMessage(len(affectedPodUIDs), resourceTypeCounts)
	meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.ManagementClusterResourcePressure),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.ReasonFailedScheduling,
		Message: msg,
	})
}

// podIsUnschedulable returns true if the pod is Pending with PodScheduled=False
// and reason=Unschedulable.
func podIsUnschedulable(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodPending {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
			return true
		}
	}
	return false
}

// extractResourceTypes parses a scheduler message for "Insufficient <resource>"
// patterns and increments the count for each resource type found. Only the
// resource type is extracted — node names, counts, and other internals are
// excluded.
func extractResourceTypes(message string, counts map[string]int) {
	matches := insufficientResourcePattern.FindAllStringSubmatch(message, -1)
	for _, m := range matches {
		counts["Insufficient "+m[1]]++
	}
}

// buildResourcePressureMessage constructs the condition message:
// "3 pods with FailedScheduling: 2 Insufficient cpu, 1 Insufficient memory"
// If no resource types were extracted, falls back to a count-only message.
func buildResourcePressureMessage(podCount int, resourceTypeCounts map[string]int) string {
	if len(resourceTypeCounts) == 0 {
		return fmt.Sprintf("%d pods with FailedScheduling", podCount)
	}

	// Sort resource types for deterministic output.
	sorted := make([]string, 0, len(resourceTypeCounts))
	for name := range resourceTypeCounts {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	// Format each resource type with its count.
	parts := make([]string, len(sorted))
	for i, name := range sorted {
		parts[i] = fmt.Sprintf("%d %s", resourceTypeCounts[name], name)
	}

	msg := fmt.Sprintf("%d pods with FailedScheduling: %s", podCount, strings.Join(parts, ", "))
	if len(msg) > maxResourcePressureMessageLen {
		msg = msg[:maxResourcePressureMessageLen-3] + "..."
	}
	return msg
}
