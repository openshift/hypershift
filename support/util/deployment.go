package util

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
)

func IsDeploymentReady(ctx context.Context, deployment *appsv1.Deployment) bool {
	if *deployment.Spec.Replicas != deployment.Status.AvailableReplicas ||
		*deployment.Spec.Replicas != deployment.Status.ReadyReplicas ||
		*deployment.Spec.Replicas != deployment.Status.UpdatedReplicas ||
		*deployment.Spec.Replicas != deployment.Status.Replicas ||
		deployment.Status.UnavailableReplicas != 0 ||
		deployment.ObjectMeta.Generation != deployment.Status.ObservedGeneration {
		return false
	}

	return true
}

func IsStatefulSetReady(ctx context.Context, statefulSet *appsv1.StatefulSet) bool {
	if *statefulSet.Spec.Replicas != statefulSet.Status.AvailableReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.ReadyReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.UpdatedReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.Replicas ||
		statefulSet.ObjectMeta.Generation != statefulSet.Status.ObservedGeneration {
		return false
	}

	return true
}
