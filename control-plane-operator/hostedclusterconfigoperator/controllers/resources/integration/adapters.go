package integration

import (
	"context"

	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type clusterAdapter struct {
	client         client.Client
	createOrUpdate upsert.CreateOrUpdateFN
}

// HostedCluster is the hosted-cluster side of the control-plane integration projections.
type HostedCluster struct{ clusterAdapter }

// ControlPlane is the control-plane side of the projections. It intentionally exposes only
// reads and upserts; the projections never delete or update status in the control plane.
type ControlPlane struct{ clusterAdapter }

// NewHostedCluster returns the hosted-cluster adapter over target and createOrUpdate.
func NewHostedCluster(target client.Client, createOrUpdate upsert.CreateOrUpdateFN) HostedCluster {
	return HostedCluster{clusterAdapter{client: target, createOrUpdate: createOrUpdate}}
}

// NewControlPlane returns the control-plane adapter over target and createOrUpdate.
func NewControlPlane(target client.Client, createOrUpdate upsert.CreateOrUpdateFN) ControlPlane {
	return ControlPlane{clusterAdapter{client: target, createOrUpdate: createOrUpdate}}
}

func (target clusterAdapter) get(ctx context.Context, key client.ObjectKey, object client.Object) error {
	return target.client.Get(ctx, key, object)
}

func (target clusterAdapter) upsert(ctx context.Context, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return target.createOrUpdate(ctx, target.client, object, mutate)
}

func (target HostedCluster) delete(ctx context.Context, object client.Object) error {
	return target.client.Delete(ctx, object)
}
