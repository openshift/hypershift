package util

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LookupActiveContainerImage returns the image for a specified container in a specified pod
func LookupActiveContainerImage(ctx context.Context, clientReader client.Reader, pod *corev1.Pod, containerName string) (string, error) {
	err := clientReader.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	if err != nil {
		return "", fmt.Errorf("failed to get pod: %w", err)
	}
	for _, container := range pod.Spec.Containers {
		// can't use downward API to pass an image id so need to look it up
		if container.Name == containerName {
			return container.Image, nil
		}
	}
	return "", fmt.Errorf("couldn't locate image id in pod")
}
