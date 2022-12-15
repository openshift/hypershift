package util

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func IsStatefulSetReady(ctx context.Context, c crclient.Client, statefulSet *appsv1.StatefulSet) (bool, error) {
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(statefulSet), statefulSet); err != nil {
		return false, fmt.Errorf("failed to fetch %s statefulset: %w", statefulSet.Name, err)
	}

	if *statefulSet.Spec.Replicas != statefulSet.Status.AvailableReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.ReadyReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.UpdatedReplicas ||
		statefulSet.ObjectMeta.Generation > statefulSet.Status.ObservedGeneration {
		return false, nil
	}

	return true, nil
}
