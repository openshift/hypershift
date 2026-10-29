//go:build e2ev2

package tests

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

func TestImageMirroringCheckEmptyIDMS(t *testing.T) {
	idms := func(name, namespace string, mirrors ...configv1.ImageDigestMirrors) *configv1.ImageDigestMirrorSet {
		return &configv1.ImageDigestMirrorSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       configv1.ImageDigestMirrorSetSpec{ImageDigestMirrors: mirrors},
		}
	}
	mapping := configv1.ImageDigestMirrors{
		Source:  "mirror-source.hypershift-e2e.invalid/libpod/registry",
		Mirrors: []configv1.ImageMirror{"mirror.apps.ci.hypershift.dev/libpod/registry"},
	}
	withWorkerLabel := idms("cluster", "")
	withWorkerLabel.Labels = map[string]string{imageMirroringWorkerLabel: "worker"}
	tests := []struct {
		name       string
		objects    []*configv1.ImageDigestMirrorSet
		wantDone   bool
		wantReason string
	}{
		{name: "When there are no objects, it should keep polling", wantReason: "exactly one"},
		{name: "When the object is nil, it should keep polling", objects: []*configv1.ImageDigestMirrorSet{nil}, wantReason: "exactly one"},
		{name: "When there are two objects, it should keep polling", objects: []*configv1.ImageDigestMirrorSet{idms("cluster", ""), idms("cluster", "")}, wantReason: "exactly one"},
		{name: "When the object has the wrong name, it should keep polling", objects: []*configv1.ImageDigestMirrorSet{idms("other", "")}, wantReason: "/other"},
		{name: "When the object is namespaced, it should keep polling", objects: []*configv1.ImageDigestMirrorSet{idms("cluster", "default")}, wantReason: "default/cluster"},
		{name: "When mappings are still present, it should keep polling and list them", objects: []*configv1.ImageDigestMirrorSet{idms("cluster", "", mapping)}, wantReason: "mirror-source.hypershift-e2e.invalid/libpod/registry"},
		{name: "When the object has no mappings, it should be done", objects: []*configv1.ImageDigestMirrorSet{idms("cluster", "")}, wantDone: true, wantReason: "no mappings"},
		{name: "When the empty object keeps the worker label, it should be done", objects: []*configv1.ImageDigestMirrorSet{withWorkerLabel}, wantDone: true, wantReason: "no mappings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, reason := imageMirroringCheckEmptyIDMS(tt.objects)
			if done != tt.wantDone || !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("got %v %q, want %v containing %q", done, reason, tt.wantDone, tt.wantReason)
			}
		})
	}
}

func TestImageMirroringCheckNodePoolBaseline(t *testing.T) {
	nodePool := func(change func(*hyperv1.NodePool)) *hyperv1.NodePool {
		np := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "image-mirroring-us-east-1a"},
			Spec: hyperv1.NodePoolSpec{
				Replicas:   ptr.To[int32](1),
				Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
			},
			Status: hyperv1.NodePoolStatus{Conditions: []hyperv1.NodePoolCondition{
				{Type: hyperv1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
				{Type: hyperv1.NodePoolUpdatingConfigConditionType, Status: corev1.ConditionFalse},
			}},
		}
		if change != nil {
			change(np)
		}
		return np
	}
	tests := []struct {
		name    string
		np      *hyperv1.NodePool
		wantErr string
	}{
		{name: "When the NodePool is healthy, idle and Replace, it should pass", np: nodePool(nil)},
		{name: "When UpdatingConfig is absent, it should pass", np: nodePool(func(np *hyperv1.NodePool) { np.Status.Conditions = np.Status.Conditions[:1] })},
		{name: "When the NodePool is nil, it should fail", wantErr: "nil"},
		{name: "When the upgrade type is InPlace, it should fail", np: nodePool(func(np *hyperv1.NodePool) { np.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace }), wantErr: "upgradeType \"InPlace\""},
		{name: "When replicas are nil, it should fail", np: nodePool(func(np *hyperv1.NodePool) { np.Spec.Replicas = nil }), wantErr: "fixed replica"},
		{name: "When replicas are zero, it should fail", np: nodePool(func(np *hyperv1.NodePool) { np.Spec.Replicas = ptr.To[int32](0) }), wantErr: "fixed replica"},
		{name: "When AllNodesHealthy is missing, it should fail", np: nodePool(func(np *hyperv1.NodePool) { np.Status.Conditions = np.Status.Conditions[1:] }), wantErr: "no AllNodesHealthy"},
		{name: "When AllNodesHealthy is False, it should fail", np: nodePool(func(np *hyperv1.NodePool) {
			np.Status.Conditions[0].Status = corev1.ConditionFalse
			np.Status.Conditions[0].Message = "machine not ready"
		}), wantErr: "machine not ready"},
		{name: "When UpdatingConfig is already True, it should fail", np: nodePool(func(np *hyperv1.NodePool) { np.Status.Conditions[1].Status = corev1.ConditionTrue }), wantErr: "already has UpdatingConfig=True"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := imageMirroringCheckNodePoolBaseline(tt.np)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func imageMirroringTestNode(name string, ready corev1.ConditionStatus) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
	}
}

func TestImageMirroringNodeReady(t *testing.T) {
	ready := imageMirroringTestNode("a", corev1.ConditionTrue)
	notReady := imageMirroringTestNode("a", corev1.ConditionFalse)
	unknown := imageMirroringTestNode("a", corev1.ConditionUnknown)
	pressureOnly := corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue}}}}
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{name: "When the node is Ready, it should return true", node: &ready, want: true},
		{name: "When the node is not Ready, it should return false", node: &notReady},
		{name: "When the node readiness is Unknown, it should return false", node: &unknown},
		{name: "When the node has no Ready condition, it should return false", node: &pressureOnly},
		{name: "When the node is nil, it should return false", node: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageMirroringNodeReady(tt.node); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImageMirroringPoolNodeNames(t *testing.T) {
	tests := []struct {
		name  string
		nodes []corev1.Node
		want  []string
	}{
		{name: "When nodes are Ready, it should return their names", nodes: []corev1.Node{imageMirroringTestNode("a", corev1.ConditionTrue), imageMirroringTestNode("b", corev1.ConditionTrue)}, want: []string{"a", "b"}},
		{name: "When a node is not Ready, it should leave it out", nodes: []corev1.Node{imageMirroringTestNode("a", corev1.ConditionFalse), imageMirroringTestNode("b", corev1.ConditionUnknown), imageMirroringTestNode("c", corev1.ConditionTrue)}, want: []string{"c"}},
		{name: "When a node has no Ready condition, it should leave it out", nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "a"}}}, want: []string{}},
		{name: "When there are no nodes, it should return an empty set", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sets.List(imageMirroringPoolNodeNames(tt.nodes)); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImageMirroringNodesReplaced(t *testing.T) {
	node := func(name string, ready corev1.ConditionStatus) *corev1.Node {
		n := imageMirroringTestNode(name, ready)
		return &n
	}
	baseline := sets.New("old-1")
	tests := []struct {
		name       string
		baseline   sets.Set[string]
		nodes      []*corev1.Node
		replicas   int32
		wantDone   bool
		wantReason string
		wantErr    string
	}{
		{name: "When every node is new and Ready, it should be done", baseline: baseline, nodes: []*corev1.Node{node("new-1", corev1.ConditionTrue)}, replicas: 1, wantDone: true, wantReason: "replaced"},
		{name: "When a baseline node is still present, it should keep polling", baseline: baseline, nodes: []*corev1.Node{node("old-1", corev1.ConditionTrue), node("new-1", corev1.ConditionTrue)}, replicas: 1, wantReason: "[old-1] still present"},
		{name: "When a baseline node is the only node, it should keep polling", baseline: baseline, nodes: []*corev1.Node{node("old-1", corev1.ConditionTrue)}, replicas: 1, wantReason: "still present"},
		{name: "When there are fewer nodes than replicas, it should keep polling", baseline: baseline, nodes: []*corev1.Node{node("new-1", corev1.ConditionTrue)}, replicas: 2, wantReason: "expected 2 nodes, got 1"},
		{name: "When there are no nodes, it should keep polling", baseline: baseline, replicas: 1, wantReason: "expected 1 nodes, got 0"},
		{name: "When there are extra nodes, it should keep polling", baseline: baseline, nodes: []*corev1.Node{node("new-1", corev1.ConditionTrue), node("new-2", corev1.ConditionTrue)}, replicas: 1, wantReason: "expected 1 nodes, got 2"},
		{name: "When a new node is not Ready, it should keep polling", baseline: baseline, nodes: []*corev1.Node{node("new-1", corev1.ConditionFalse)}, replicas: 1, wantReason: "[new-1] are not Ready"},
		{name: "When the baseline is empty, it should fail terminally", baseline: sets.New[string](), nodes: []*corev1.Node{node("new-1", corev1.ConditionTrue)}, replicas: 1, wantErr: "empty baseline"},
		{name: "When replicas are zero, it should fail terminally", baseline: baseline, replicas: 0, wantErr: "at least one replica"},
		{name: "When a node is nil, it should fail terminally", baseline: baseline, nodes: []*corev1.Node{nil}, replicas: 1, wantErr: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringNodesReplaced(tt.baseline, tt.nodes, tt.replicas)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || done {
					t.Fatalf("expected terminal error containing %q, got %v %v", tt.wantErr, done, err)
				}
				return
			}
			if err != nil || done != tt.wantDone || !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("got %v %q %v, want %v containing %q", done, reason, err, tt.wantDone, tt.wantReason)
			}
		})
	}
}

func TestImageMirroringCheckRemovedPullRejectedPod(t *testing.T) {
	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantDone   bool
		wantReason string
		wantErr    string
	}{
		{name: "When the image pull fails, it should be done", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ErrImagePull", "lookup mirror-source.hypershift-e2e.invalid: no such host")), wantDone: true, wantReason: "no such host"},
		{name: "When the image pull backs off, it should be done", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ImagePullBackOff", "")), wantDone: true, wantReason: "ImagePullBackOff"},
		{name: "When the pod is Pending without container statuses, it should keep polling", pod: imageMirroringTestPod(corev1.PodPending, nil), wantReason: "has no status"},
		{name: "When the container is creating, it should keep polling", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("ContainerCreating", "")), wantReason: "ContainerCreating"},
		{name: "When the container is running, it should fail terminally", pod: imageMirroringTestPod(corev1.PodRunning, imageMirroringRunningState()), wantErr: "still obtained the image after imageContentSources removal"},
		{name: "When the container terminated, it should fail terminally", pod: imageMirroringTestPod(corev1.PodSucceeded, imageMirroringTerminatedState(0)), wantErr: "still obtained the image after imageContentSources removal"},
		{name: "When the container config cannot be created, it should fail terminally", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("CreateContainerError", "")), wantErr: "still obtained the image after imageContentSources removal"},
		{name: "When the container exited nonzero, it should fail terminally", pod: imageMirroringTestPod(corev1.PodFailed, imageMirroringTerminatedState(1)), wantErr: "still obtained the image after imageContentSources removal"},
		{name: "When the image reference is rejected without a pull, it should fail terminally", pod: imageMirroringTestPod(corev1.PodPending, imageMirroringWaitingState("InvalidImageName", "")), wantErr: "rejected without a pull attempt"},
		{name: "When the pod Failed without container statuses, it should fail terminally", pod: imageMirroringTestPod(corev1.PodFailed, nil), wantErr: "unexpected phase after imageContentSources removal"},
		{name: "When the pod is nil, it should fail terminally", pod: nil, wantErr: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, reason, err := imageMirroringCheckRemovedPullRejectedPod(tt.pod)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || done {
					t.Fatalf("expected terminal error containing %q, got %v %v", tt.wantErr, done, err)
				}
				return
			}
			if err != nil || done != tt.wantDone || !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("got %v %q %v, want %v containing %q", done, reason, err, tt.wantDone, tt.wantReason)
			}
		})
	}
}

func TestImageMirroringCheckNodePoolConditions(t *testing.T) {
	nodePool := func(conditions ...hyperv1.NodePoolCondition) *hyperv1.NodePool {
		return &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "image-mirroring-us-east-1a"}, Status: hyperv1.NodePoolStatus{Conditions: conditions}}
	}
	condition := func(conditionType string, status corev1.ConditionStatus) hyperv1.NodePoolCondition {
		return hyperv1.NodePoolCondition{Type: conditionType, Status: status, Reason: "AsExpected"}
	}
	finished := map[string]corev1.ConditionStatus{
		hyperv1.NodePoolUpdatingConfigConditionType:  corev1.ConditionFalse,
		hyperv1.NodePoolAllNodesHealthyConditionType: corev1.ConditionTrue,
	}
	tests := []struct {
		name       string
		np         *hyperv1.NodePool
		want       map[string]corev1.ConditionStatus
		wantDone   bool
		wantReason string
	}{
		{name: "When every wanted condition matches, it should be done", np: nodePool(condition(hyperv1.NodePoolUpdatingConfigConditionType, corev1.ConditionFalse), condition(hyperv1.NodePoolAllNodesHealthyConditionType, corev1.ConditionTrue)), want: finished, wantDone: true},
		{name: "When a wanted condition has another status, it should keep polling and report it", np: nodePool(condition(hyperv1.NodePoolUpdatingConfigConditionType, corev1.ConditionTrue), condition(hyperv1.NodePoolAllNodesHealthyConditionType, corev1.ConditionTrue)), want: finished, wantReason: "UpdatingConfig=True"},
		{name: "When a wanted condition is missing, it should keep polling and report it", np: nodePool(condition(hyperv1.NodePoolUpdatingConfigConditionType, corev1.ConditionFalse)), want: finished, wantReason: "no AllNodesHealthy condition"},
		{name: "When the rollout has started, it should match UpdatingConfig True", np: nodePool(condition(hyperv1.NodePoolUpdatingConfigConditionType, corev1.ConditionTrue)), want: map[string]corev1.ConditionStatus{hyperv1.NodePoolUpdatingConfigConditionType: corev1.ConditionTrue}, wantDone: true},
		{name: "When the NodePool is nil, it should keep polling", np: nil, want: finished, wantReason: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done, reason := imageMirroringCheckNodePoolConditions(tt.np, tt.want)
			if done != tt.wantDone || !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("got %v %q, want %v containing %q", done, reason, tt.wantDone, tt.wantReason)
			}
		})
	}
}
