package apiproviders

import (
	"context"
	"fmt"
	capiibmv1 "github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/api/v1alpha4"
	capiv1alpha4 "sigs.k8s.io/cluster-api/api/v1alpha4"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/upsert"
)

type IBMPlatformReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
}

func NewIBMAPlatformReconciler(cl client.Client, cuProv upsert.CreateOrUpdateProvider) *IBMPlatformReconciler {
	return &IBMPlatformReconciler{
		Client:                 cl,
		CreateOrUpdateProvider: cuProv,
	}
}

func (r IBMPlatformReconciler) ReconclieCred(_ context.Context, _ *hyperv1.HostedCluster, _ string) error {
	return nil
}

// ReconcileSecret
func (h IBMPlatformReconciler) ReconcileSecret(_ context.Context, _ *hyperv1.HostedCluster, _ string) error {
	return nil
}

func (h IBMPlatformReconciler) GetInfraCR(ctx context.Context, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, cpNamespace string) (client.Object, error) {
	// Reconcile external IBM Cloud Cluster
	ibmCluster := controlplaneoperator.IBMCloudCluster(cpNamespace, hcluster.Name)
	_, err := h.CreateOrUpdate(ctx, h.Client, ibmCluster, func() error {
		return reconcileIBMCloudCluster(ibmCluster, hcluster, hcp.Status.ControlPlaneEndpoint)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to reconcile IBMCluster: %w", err)
	}

	return ibmCluster, nil
}

func (r *IBMPlatformReconciler) ReconcileCAPIProvider(_ context.Context, _ *hyperv1.HostedCluster) error {
	return nil
}

func reconcileIBMCloudCluster(ibmCluster *capiibmv1.IBMVPCCluster, hcluster *hyperv1.HostedCluster, apiEndpoint hyperv1.APIEndpoint) error {
	ibmCluster.Annotations = map[string]string{
		HostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
		capiv1.ManagedByAnnotation: "external",
	}

	// Set the values for upper level controller
	ibmCluster.Status.Ready = true
	ibmCluster.Spec.ControlPlaneEndpoint = capiv1alpha4.APIEndpoint{
		Host: apiEndpoint.Host,
		Port: apiEndpoint.Port,
	}
	return nil
}
