package podspec

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxDiagnosticMessageLen      = 256
	deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
)

// DiagnoseWorkloadPodFailure inspects current-revision pods for the given
// Deployment or StatefulSet workload and returns a structured reason and
// message if a pod-level failure is detected. When the workload is already
// ready (IsDeploymentReady / IsStatefulSetReady), it returns ("", "") without
// issuing any API calls. Pods belonging to older ReplicaSets or StatefulSet
// revisions are ignored so stale failures cannot mask an active rollout or
// recovery. It uses a short timeout to avoid blocking reconciliation. Returns
// ("", "") if no diagnosable failure is found or on error.
func DiagnoseWorkloadPodFailure(ctx context.Context, c client.Client, workload client.Object) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		sel       labels.Selector
		namespace string
		err       error
	)
	switch w := workload.(type) {
	case *appsv1.Deployment:
		if IsDeploymentReady(ctx, w) {
			return "", ""
		}
		namespace = w.Namespace
		sel, err = currentReplicaSetSelector(ctx, c, w)
	case *appsv1.StatefulSet:
		if IsStatefulSetReady(ctx, w) {
			return "", ""
		}
		namespace = w.Namespace
		sel, err = currentStatefulSetRevisionSelector(w)
	default:
		return "", ""
	}
	if err != nil || sel == nil {
		return "", ""
	}
	return diagnosePodsBySelector(ctx, c, namespace, sel)
}

// currentReplicaSetSelector finds the ReplicaSet owned by deployment matching
// its current revision annotation and returns its label selector (which
// includes pod-template-hash). Returns (nil, nil) if no matching ReplicaSet is
// found.
func currentReplicaSetSelector(ctx context.Context, c client.Client, deployment *appsv1.Deployment) (labels.Selector, error) {
	if deployment.Spec.Selector == nil {
		return nil, nil
	}
	currentRev := deployment.Annotations[deploymentRevisionAnnotation]
	if currentRev == "" {
		return nil, nil
	}
	depSel, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	rsList := &appsv1.ReplicaSetList{}
	if err := c.List(ctx, rsList, client.InNamespace(deployment.Namespace), client.MatchingLabelsSelector{Selector: depSel}); err != nil {
		return nil, err
	}
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !isOwnedByDeployment(rs, deployment) {
			continue
		}
		if rs.Annotations[deploymentRevisionAnnotation] == currentRev && rs.Spec.Selector != nil {
			return metav1.LabelSelectorAsSelector(rs.Spec.Selector)
		}
	}
	return nil, nil
}

func isOwnedByDeployment(rs *appsv1.ReplicaSet, deployment *appsv1.Deployment) bool {
	if deployment.UID != "" {
		return metav1.IsControlledBy(rs, deployment)
	}
	for _, ref := range rs.OwnerReferences {
		if ref.Controller != nil && *ref.Controller && ref.Kind == "Deployment" && ref.Name == deployment.Name {
			return true
		}
	}
	return false
}

// currentStatefulSetRevisionSelector returns a label selector scoping pods to
// the StatefulSet's active revision (UpdateRevision, falling back to
// CurrentRevision). Returns (nil, nil) if no revision is set.
func currentStatefulSetRevisionSelector(sts *appsv1.StatefulSet) (labels.Selector, error) {
	if sts.Spec.Selector == nil {
		return nil, nil
	}
	revision := sts.Status.UpdateRevision
	if revision == "" {
		revision = sts.Status.CurrentRevision
	}
	if revision == "" {
		return nil, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return nil, err
	}
	req, err := labels.NewRequirement(appsv1.ControllerRevisionHashLabelKey, selection.Equals, []string{revision})
	if err != nil {
		return nil, err
	}
	return sel.Add(*req), nil
}

func diagnosePodsBySelector(ctx context.Context, c client.Client, namespace string, sel labels.Selector) (string, string) {
	if sel == nil {
		return "", ""
	}

	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return "", ""
	}

	// Sort by name so the first failing pod is deterministic and condition
	// messages remain stable across reconciles.
	sort.Slice(podList.Items, func(i, j int) bool {
		return podList.Items[i].Name < podList.Items[j].Name
	})

	// First pass: check all eligible pods for scheduling failures so that a
	// CrashLoopBackOff or ImagePullBackOff on an earlier pod cannot mask an
	// Unschedulable replica that signals management-cluster resource pressure.
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == "Unschedulable" {
				return hyperv1.PodSchedulingFailedReason, truncateMessage(sanitizeSchedulerMessage(pod.Name, cond.Message))
			}
		}
	}

	// Second pass: check container and init container statuses for CrashLoopBackOff
	// and ImagePullBackOff.
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				switch cs.State.Waiting.Reason {
				case "CrashLoopBackOff":
					msg := fmt.Sprintf("Pod %s container %s: CrashLoopBackOff", pod.Name, cs.Name)
					if cs.LastTerminationState.Terminated != nil {
						msg += fmt.Sprintf(" (%s)", cs.LastTerminationState.Terminated.Reason)
					}
					return hyperv1.CrashLoopBackOffReason, truncateMessage(msg)
				case "ImagePullBackOff", "ErrImagePull":
					return hyperv1.ImagePullBackOffReason, truncateMessage(fmt.Sprintf("Pod %s container %s: %s", pod.Name, cs.Name, cs.State.Waiting.Reason))
				}
			}
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil {
				switch cs.State.Waiting.Reason {
				case "CrashLoopBackOff":
					return hyperv1.CrashLoopBackOffReason, truncateMessage(fmt.Sprintf("Pod %s init container %s: CrashLoopBackOff", pod.Name, cs.Name))
				case "ImagePullBackOff", "ErrImagePull":
					return hyperv1.ImagePullBackOffReason, truncateMessage(fmt.Sprintf("Pod %s init container %s: %s", pod.Name, cs.Name, cs.State.Waiting.Reason))
				}
			}
		}
	}

	return "", ""
}

func truncateMessage(msg string) string {
	if len(msg) > maxDiagnosticMessageLen {
		return msg[:maxDiagnosticMessageLen-3] + "..."
	}
	return msg
}

// insufficientResourcePattern matches "Insufficient <resource>" in scheduler messages.
// The resource name allows word characters, hyphens, dots, and slashes to cover
// extended resources like "example.com/gpu" or "hugepages-2Mi". The pattern
// requires the name to end with a word character so that trailing punctuation
// (periods, commas) from the scheduler message is excluded.
var insufficientResourcePattern = regexp.MustCompile(`Insufficient\s+([\w]+(?:[.\-/][\w]+)*)`)

// sanitizeSchedulerMessage extracts only resource-type information from a raw
// scheduler message to avoid leaking management cluster details (node names,
// IPs, cluster size) into conditions visible to hosted cluster users.
func sanitizeSchedulerMessage(podName, rawMessage string) string {
	matches := insufficientResourcePattern.FindAllStringSubmatch(rawMessage, -1)
	if len(matches) == 0 {
		return fmt.Sprintf("Pod %s is unschedulable", podName)
	}
	seen := make(map[string]struct{})
	var resources []string
	for _, m := range matches {
		res := m[1]
		if _, ok := seen[res]; !ok {
			seen[res] = struct{}{}
			resources = append(resources, res)
		}
	}
	return fmt.Sprintf("Pod %s is unschedulable: Insufficient %s", podName, strings.Join(resources, ", Insufficient "))
}
