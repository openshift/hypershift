package podspec

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsDeploymentReady(t *testing.T) {
	tests := []struct {
		deployment *appsv1.Deployment
		ready      bool
	}{
		{
			// Positive path - all replicas updated, available, ready
			deployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:       "positive-path",
					Generation: 1,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: true,
		},
		{
			// Negative path - replicas not yet updated
			deployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-1",
					Generation: 1,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					Replicas:           3,
					UpdatedReplicas:    2,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
		{
			// Negative path - replicas not yet available
			deployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-2",
					Generation: 1,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  2,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
		{
			// Negative path - generation mismatch
			deployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-3",
					Generation: 2,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
		{
			// Negative path - surging upgrade
			deployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-4",
					Generation: 2,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					Replicas:            3,
					UpdatedReplicas:     3,
					AvailableReplicas:   3,
					ReadyReplicas:       3,
					ObservedGeneration:  2,
					UnavailableReplicas: 1,
				},
			},
			ready: false,
		},
	}
	for _, tt := range tests {
		ready := IsDeploymentReady(t.Context(), tt.deployment)
		if ready != tt.ready {
			t.Errorf("IsDeploymentReady() deployment %s got ready %t, expected %t", tt.deployment.Name, ready, tt.ready)
			return
		}
	}
}

func TestIsStatefulSetReady(t *testing.T) {
	tests := []struct {
		statefulSet *appsv1.StatefulSet
		ready       bool
	}{
		{
			// Positive path - all replicas updated, available, ready
			statefulSet: &appsv1.StatefulSet{
				ObjectMeta: v1.ObjectMeta{
					Name:       "positive-path",
					Generation: 1,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: true,
		},
		{
			// Negative path - replicas not yet updated
			statefulSet: &appsv1.StatefulSet{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-1",
					Generation: 1,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:           3,
					UpdatedReplicas:    2,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
		{
			// Negative path - replicas not yet available
			statefulSet: &appsv1.StatefulSet{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-2",
					Generation: 1,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  2,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
		{
			// Negative path - generation mismatch
			statefulSet: &appsv1.StatefulSet{
				ObjectMeta: v1.ObjectMeta{
					Name:       "negative-path-3",
					Generation: 2,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  3,
					ReadyReplicas:      3,
					ObservedGeneration: 1,
				},
			},
			ready: false,
		},
	}
	for _, tt := range tests {
		ready := IsStatefulSetReady(t.Context(), tt.statefulSet)
		if ready != tt.ready {
			t.Errorf("IsStatefulSetReady() statefulset %s got ready %t, expected %t", tt.statefulSet.Name, ready, tt.ready)
			return
		}
	}
}

func TestHasTerminatingPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	selector := &v1.LabelSelector{
		MatchLabels: map[string]string{"app": "test"},
	}

	tests := []struct {
		name           string
		selector       *v1.LabelSelector
		pods           []corev1.Pod
		deletePod      string
		expectedResult bool
		expectError    bool
	}{
		{
			name:     "nil selector returns false",
			selector: nil,
			pods: []corev1.Pod{
				{ObjectMeta: v1.ObjectMeta{Name: "pod-1", Namespace: "ns", Labels: map[string]string{"app": "test"}}},
			},
			expectedResult: false,
		},
		{
			name:           "no pods returns false",
			selector:       selector,
			pods:           nil,
			expectedResult: false,
		},
		{
			name:     "all pods running returns false",
			selector: selector,
			pods: []corev1.Pod{
				{ObjectMeta: v1.ObjectMeta{Name: "pod-1", Namespace: "ns", Labels: map[string]string{"app": "test"}}},
				{ObjectMeta: v1.ObjectMeta{Name: "pod-2", Namespace: "ns", Labels: map[string]string{"app": "test"}}},
			},
			expectedResult: false,
		},
		{
			name:     "one terminating pod returns true",
			selector: selector,
			pods: []corev1.Pod{
				{ObjectMeta: v1.ObjectMeta{Name: "pod-1", Namespace: "ns", Labels: map[string]string{"app": "test"}}},
				{ObjectMeta: v1.ObjectMeta{Name: "pod-2", Namespace: "ns", Labels: map[string]string{"app": "test"}, Finalizers: []string{"keep-alive"}}},
			},
			deletePod:      "pod-2",
			expectedResult: true,
		},
		{
			name: "malformed selector returns error",
			selector: &v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{Key: "app", Operator: "InvalidOp", Values: []string{"v"}},
				},
			},
			expectedResult: false,
			expectError:    true,
		},
		{
			name:     "pods not matching selector are ignored",
			selector: selector,
			pods: []corev1.Pod{
				{ObjectMeta: v1.ObjectMeta{Name: "pod-1", Namespace: "ns", Labels: map[string]string{"app": "other"}, Finalizers: []string{"keep-alive"}}},
			},
			deletePod:      "pod-1",
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.pods {
				builder = builder.WithObjects(&tt.pods[i])
			}
			c := builder.Build()

			if tt.deletePod != "" {
				pod := &corev1.Pod{}
				pod.Name = tt.deletePod
				pod.Namespace = "ns"
				if err := c.Delete(t.Context(), pod); err != nil {
					t.Fatalf("failed to delete pod %s: %v", tt.deletePod, err)
				}
			}

			result, err := HasTerminatingPods(t.Context(), c, "ns", tt.selector)
			if tt.expectError {
				if err == nil {
					t.Errorf("HasTerminatingPods() expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("HasTerminatingPods() unexpected error: %v", err)
				return
			}
			if result != tt.expectedResult {
				t.Errorf("HasTerminatingPods() %s got %t, expected %t", tt.name, result, tt.expectedResult)
			}
		})
	}
}
