//go:build e2ev2

package tests

import (
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// imageMirroringCheckEmptyIDMS is done when the hosted ImageDigestMirrorSet/cluster
// still exists and carries no mappings. The HCCO keeps the object and empties it
// when imageContentSources is removed; its labels are not part of this check.
func imageMirroringCheckEmptyIDMS(objects []*configv1.ImageDigestMirrorSet) (bool, string) {
	if len(objects) != 1 || objects[0] == nil {
		return false, "expected exactly one nonnil ImageDigestMirrorSet/cluster"
	}
	object := objects[0]
	if object.Name != "cluster" || object.Namespace != "" {
		return false, fmt.Sprintf("expected cluster-scoped ImageDigestMirrorSet/cluster, got %s/%s", object.Namespace, object.Name)
	}
	if len(object.Spec.ImageDigestMirrors) != 0 {
		return false, fmt.Sprintf("ImageDigestMirrorSet/cluster still has mappings after imageContentSources removal: %+v", object.Spec.ImageDigestMirrors)
	}
	return true, "ImageDigestMirrorSet/cluster has no mappings"
}

// imageMirroringCheckNodePoolBaseline verifies that a NodePool can prove a
// removal rollout: Replace upgrades, fixed replicas, all nodes healthy and no
// config update already in progress.
func imageMirroringCheckNodePoolBaseline(np *hyperv1.NodePool) error {
	if np == nil {
		return fmt.Errorf("NodePool must not be nil")
	}
	identity := fmt.Sprintf("NodePool %s/%s", np.Namespace, np.Name)
	if np.Spec.Management.UpgradeType != hyperv1.UpgradeTypeReplace {
		return fmt.Errorf("%s has upgradeType %q; the node replacement check requires %q", identity, np.Spec.Management.UpgradeType, hyperv1.UpgradeTypeReplace)
	}
	if np.Spec.Replicas == nil || *np.Spec.Replicas < 1 {
		return fmt.Errorf("%s must have at least one fixed replica", identity)
	}
	healthy := false
	for _, cond := range np.Status.Conditions {
		switch cond.Type {
		case hyperv1.NodePoolAllNodesHealthyConditionType:
			if cond.Status != corev1.ConditionTrue {
				return fmt.Errorf("%s condition %s=%s: %s", identity, cond.Type, cond.Status, cond.Message)
			}
			healthy = true
		case hyperv1.NodePoolUpdatingConfigConditionType:
			if cond.Status == corev1.ConditionTrue {
				return fmt.Errorf("%s already has %s=True: %s", identity, cond.Type, cond.Message)
			}
		}
	}
	if !healthy {
		return fmt.Errorf("%s has no %s condition", identity, hyperv1.NodePoolAllNodesHealthyConditionType)
	}
	return nil
}

func imageMirroringNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// imageMirroringPoolNodeNames returns the names of the Ready nodes.
func imageMirroringPoolNodeNames(nodes []corev1.Node) sets.Set[string] {
	names := sets.New[string]()
	for i := range nodes {
		if imageMirroringNodeReady(&nodes[i]) {
			names.Insert(nodes[i].Name)
		}
	}
	return names
}

// imageMirroringNodesReplaced is done when the pool has exactly replicas nodes,
// all Ready, and none of them was part of the pre-removal baseline. An empty
// baseline cannot prove a replacement and is a terminal error.
func imageMirroringNodesReplaced(baseline sets.Set[string], nodes []*corev1.Node, replicas int32) (bool, string, error) {
	if baseline.Len() == 0 {
		return false, "", fmt.Errorf("empty baseline node set cannot prove a node replacement")
	}
	if replicas < 1 {
		return false, "", fmt.Errorf("expected at least one replica, got %d", replicas)
	}
	var names, notReady, stale []string
	for _, node := range nodes {
		if node == nil {
			return false, "", fmt.Errorf("node must not be nil")
		}
		names = append(names, node.Name)
		if baseline.Has(node.Name) {
			stale = append(stale, node.Name)
		}
		if !imageMirroringNodeReady(node) {
			notReady = append(notReady, node.Name)
		}
	}
	sort.Strings(names)
	sort.Strings(notReady)
	sort.Strings(stale)
	switch {
	case len(stale) > 0:
		return false, fmt.Sprintf("pre-removal nodes %v still present (nodes %v)", stale, names), nil
	case len(nodes) != int(replicas):
		return false, fmt.Sprintf("expected %d nodes, got %d: %v", replicas, len(nodes), names), nil
	case len(notReady) > 0:
		return false, fmt.Sprintf("nodes %v are not Ready", notReady), nil
	}
	return true, fmt.Sprintf("all %d nodes %v replaced the pre-removal nodes %v", replicas, names, sets.List(baseline)), nil
}

// imageMirroringCheckRemovedPullRejectedPod is done when the container waits on
// an image pull failure after imageContentSources removal. Any sign that the
// image was obtained is a terminal error.
func imageMirroringCheckRemovedPullRejectedPod(pod *corev1.Pod) (bool, string, error) {
	if pod == nil {
		return false, "", fmt.Errorf("pod must not be nil")
	}
	summary := imageMirroringPodSummary(pod)
	status := imageMirroringContainerStatus(pod)
	if status != nil {
		switch {
		case status.State.Running != nil, status.State.Terminated != nil:
			return false, "", fmt.Errorf("by-digest pull from the mirror-only source still obtained the image after imageContentSources removal: %s", summary)
		case status.State.Waiting != nil:
			switch status.State.Waiting.Reason {
			case "ErrImagePull", "ImagePullBackOff":
				return true, summary, nil
			case "CreateContainerConfigError", "CreateContainerError":
				// The kubelet only builds the container after the image is present.
				return false, "", fmt.Errorf("by-digest pull from the mirror-only source still obtained the image after imageContentSources removal: %s", summary)
			}
		}
	}
	switch pod.Status.Phase {
	case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
		return false, "", fmt.Errorf("by-digest pod reached an unexpected phase after imageContentSources removal: %s", summary)
	}
	return false, summary, nil
}

// imageMirroringCheckNodePoolConditions is done when every wanted condition is
// present on the NodePool with the wanted status. A missing condition keeps
// polling, because the controller may not have reported it yet.
func imageMirroringCheckNodePoolConditions(np *hyperv1.NodePool, want map[string]corev1.ConditionStatus) (bool, string) {
	if np == nil {
		return false, "NodePool is nil"
	}
	types := make([]string, 0, len(want))
	for conditionType := range want {
		types = append(types, conditionType)
	}
	sort.Strings(types)
	for _, conditionType := range types {
		found := false
		for _, condition := range np.Status.Conditions {
			if condition.Type != conditionType {
				continue
			}
			found = true
			if condition.Status != want[conditionType] {
				return false, fmt.Sprintf("NodePool %s/%s condition %s=%s (reason %q, message %q), want %s",
					np.Namespace, np.Name, conditionType, condition.Status, condition.Reason, condition.Message, want[conditionType])
			}
		}
		if !found {
			return false, fmt.Sprintf("NodePool %s/%s has no %s condition, want %s", np.Namespace, np.Name, conditionType, want[conditionType])
		}
	}
	return true, ""
}
