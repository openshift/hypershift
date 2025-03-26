package util

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
		ready := IsDeploymentReady(context.TODO(), tt.deployment)
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
		ready := IsStatefulSetReady(context.TODO(), tt.statefulSet)
		if ready != tt.ready {
			t.Errorf("IsStatefulSetReady() statefulset %s got ready %t, expected %t", tt.statefulSet.Name, ready, tt.ready)
			return
		}
	}
}
