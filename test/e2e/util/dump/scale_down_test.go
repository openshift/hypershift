package dump

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestRenderPodPlacement(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-0-1.ec2.internal"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}

	daemonSetPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "openshift-monitoring",
			Name:            "node-exporter-abcde",
			OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "node-exporter"}},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Containers: []corev1.Container{{
				Name:      "node-exporter",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	// A kube-system Deployment pod, as the kas-connection-checker is: it blocks
	// scale-down of its node unless a PodDisruptionBudget allows eviction.
	systemPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "kube-system",
			Name:            "kas-connection-checker-5f7c9d8b4-abcde",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "kas-connection-checker-5f7c9d8b4"}},
		},
		Spec: corev1.PodSpec{
			NodeName: node.Name,
			Priority: ptr.To[int32](2000001000),
			Containers: []corev1.Container{{
				Name:      "connection-checker",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("5m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	mirrorPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "kube-system",
			Name:            "kube-apiserver-proxy-ip-10-0-0-1.ec2.internal",
			Annotations:     map[string]string{corev1.MirrorPodAnnotationKey: "abcdef"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Node", Name: node.Name}},
		},
		Spec:   corev1.PodSpec{NodeName: node.Name},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pendingPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "openshift-marketplace",
			Name:            "community-operators-catalog-7d9f-xyz",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "community-operators-catalog-7d9f"}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:      "registry",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m")}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}

	out := renderPodPlacement(
		[]corev1.Node{node},
		[]corev1.Pod{daemonSetPod, systemPod, mirrorPod, pendingPod},
	)

	for _, expected := range []string{
		"node ip-10-0-0-1.ec2.internal unschedulable=false ready=True",
		"nonDaemonSetPods=1 kubeSystemBlockingPods=1",
		"openshift-monitoring/node-exporter-abcde phase=Running ds=true mirror=false owner=DaemonSet/node-exporter",
		"kube-system/kas-connection-checker-5f7c9d8b4-abcde phase=Running ds=false mirror=false",
		"priority=2000001000 critical=true",
		"kube-system/kube-apiserver-proxy-ip-10-0-0-1.ec2.internal phase=Running ds=false mirror=true",
		"pending (not scheduled) pods: 1",
		"openshift-marketplace/community-operators-catalog-7d9f-xyz phase=Pending",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected the pod placement report to contain %q, got:\n%s", expected, out)
		}
	}
}

func TestRenderPodPlacementIgnoresSucceededPodsWithoutNode(t *testing.T) {
	succeeded := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-insights", Name: "periodic-gathering-abcde-xyz"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	out := renderPodPlacement(nil, []corev1.Pod{succeeded})
	if !strings.Contains(out, "pending (not scheduled) pods: 0") {
		t.Errorf("expected completed pods to be excluded from the pending list, got:\n%s", out)
	}
}
