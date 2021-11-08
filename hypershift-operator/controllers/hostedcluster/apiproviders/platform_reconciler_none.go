package apiproviders

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/upsert"
)

type NonePlatformReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
}

func (r NonePlatformReconciler) ReconclieCred(_ context.Context, _ *hyperv1.HostedCluster, _ string) error {
	return nil
}

func NewNoneAPlatformReconciler(cl client.Client, cuProv upsert.CreateOrUpdateProvider) *NonePlatformReconciler {
	return &NonePlatformReconciler{
		Client:                 cl,
		CreateOrUpdateProvider: cuProv,
	}
}

// ReconcileSecret
func (h NonePlatformReconciler) ReconcileSecret(_ context.Context, _ *hyperv1.HostedCluster, _ string) error {
	return nil
}

// TODO(alberto): for platform None implement back a "pass through" infra CR similar to externalInfraCluster.
func (h NonePlatformReconciler) GetInfraCR(ctx context.Context, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, cpNamespace string) (client.Object, error) {
	// Reconcile external AWSCluster
	awsCluster := controlplaneoperator.AWSCluster(cpNamespace, hcluster.Name)
	_, err := controllerutil.CreateOrPatch(ctx, h.Client, awsCluster, func() error {
		return reconcileAWSCluster(awsCluster, hcluster, hcp.Status.ControlPlaneEndpoint)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to reconcile AWSCluster: %w", err)
	}

	return awsCluster, nil
}

func (r *NonePlatformReconciler) ReconcileCAPIProvider(_ context.Context, _ *hyperv1.HostedCluster) error {
	return nil
}
