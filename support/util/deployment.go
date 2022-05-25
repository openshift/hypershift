package util

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func IsDeploymentReady(ctx context.Context, c crclient.Client, deployment *appsv1.Deployment) (bool, error) {
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
		return false, fmt.Errorf("failed to fetch %s deployment: %w", deployment.Name, err)
	}

	if *deployment.Spec.Replicas != deployment.Status.AvailableReplicas ||
		*deployment.Spec.Replicas != deployment.Status.ReadyReplicas ||
		*deployment.Spec.Replicas != deployment.Status.UpdatedReplicas ||
		deployment.ObjectMeta.Generation > deployment.Status.ObservedGeneration {
		return false, nil
	}

	return true, nil
}
