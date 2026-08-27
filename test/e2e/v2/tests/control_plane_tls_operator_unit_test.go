//go:build e2ev2

package tests

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestFindReadyReplacementPod(t *testing.T) {
	readyPod := func(uid string, ready bool) corev1.Pod {
		conditionStatus := corev1.ConditionFalse
		if ready {
			conditionStatus = corev1.ConditionTrue
		}
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{UID: types.UID(uid)},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: conditionStatus,
				}},
				ContainerStatuses: []corev1.ContainerStatus{{Ready: ready}},
			},
		}
	}

	tests := []struct {
		name        string
		pods        []corev1.Pod
		previousUID string
		expectedUID string
	}{
		{
			name: "When the stale pod precedes a ready replacement, it should return the replacement",
			pods: []corev1.Pod{
				readyPod("stale", true),
				readyPod("replacement", true),
			},
			previousUID: "stale",
			expectedUID: "replacement",
		},
		{
			name: "When only the stale pod is ready, it should return no pod",
			pods: []corev1.Pod{
				readyPod("stale", true),
				readyPod("replacement", false),
			},
			previousUID: "stale",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := findReadyReplacementPod(test.pods, test.previousUID)
			if test.expectedUID == "" {
				if pod != nil {
					t.Fatalf("expected no pod, got UID %s", pod.UID)
				}
				return
			}
			if pod == nil {
				t.Fatalf("expected pod UID %s, got no pod", test.expectedUID)
			}
			if string(pod.UID) != test.expectedUID {
				t.Fatalf("expected pod UID %s, got %s", test.expectedUID, pod.UID)
			}
		})
	}
}
