package extend

import (
	"context"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

func GetHyperShiftOperatorNamespace(ctx context.Context, client crclient.Client) (string, error) {
	podList := &corev1.PodList{}
	err := client.List(ctx, podList, &crclient.ListOptions{
		LabelSelector: getOperatorSelector(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list operator pods: %w", err)
	}
	if len(podList.Items) == 0 {
		return "", nil
	}
	return podList.Items[0].Namespace, nil
}

func GetPlatformType(ctx context.Context, client crclient.Client) (string, error) {
	infra := &configv1.Infrastructure{}

	err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, infra)
	if err != nil {
		return "", err
	}

	platform := ""
	if infra.Status.PlatformStatus != nil {
		platform = string(infra.Status.PlatformStatus.Type)
	}

	return strings.ToLower(platform), nil
}

func GetHypershiftOperators(ctx context.Context, client crclient.Client) ([]string, error) {
	operatorNS, err := GetHyperShiftOperatorNamespace(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get hypershift operator namespace: %w", err)
	}
	podList := &corev1.PodList{}
	if err := client.List(
		ctx,
		podList,
		crclient.InNamespace(operatorNS)); err != nil {
		return nil, fmt.Errorf("failed to list hypershift pods: %w", err)
	}

	operators := make([]string, 0, len(podList.Items))
	for _, pod := range podList.Items {
		operators = append(operators, pod.Name)
	}
	return operators, nil
}
