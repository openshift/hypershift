package apiproviders

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	HostedClusterAnnotation = "hypershift.openshift.io/cluster"
)

// PlatformReconciler is an interface for a platform specific reconciler.
type PlatformReconciler interface {
	ReconclieCred(ctx context.Context, hcluster *hyperv1.HostedCluster, cpns string) error
	ReconcileSecret(context.Context, *hyperv1.HostedCluster, string) error
	GetInfraCR(context.Context, *hyperv1.HostedCluster, *hyperv1.HostedControlPlane, string) (client.Object, error)
	ReconcileCAPIProvider(context.Context, *hyperv1.HostedCluster) error
}

type PlatformHandlers map[hyperv1.PlatformType]PlatformReconciler

func (phs PlatformHandlers) GetPlatformReconciler(platformName hyperv1.PlatformType) (PlatformReconciler, error) {
	ph, found := phs[platformName]
	if !found {
		return nil, fmt.Errorf("%s platform is not supported", platformName)
	}

	return ph, nil
}
