package controllers

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

// TODO: base domain isn't actually part of our API
func ClusterBaseDomain(client ctrl.Client, ctx context.Context, clusterName string) (string, error) {
	var dnsConfig configv1.DNS
	err := client.Get(ctx, ctrl.ObjectKey{Name: "cluster"}, &dnsConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster dns config: %w", err)
	}
	return fmt.Sprintf("%s.%s", clusterName, dnsConfig.Spec.BaseDomain), nil
}
