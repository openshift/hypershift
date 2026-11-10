package configuration

import (
	"context"

	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type clusterAdapter struct {
	client         client.Client
	createOrUpdate upsert.CreateOrUpdateFN
}
type HostedCluster struct{ clusterAdapter }
type ControlPlane struct{ clusterAdapter }

func NewHostedCluster(target client.Client, createOrUpdate upsert.CreateOrUpdateFN) HostedCluster {
	return HostedCluster{clusterAdapter{client: target, createOrUpdate: createOrUpdate}}
}
func NewControlPlane(target client.Client, createOrUpdate upsert.CreateOrUpdateFN) ControlPlane {
	return ControlPlane{clusterAdapter{client: target, createOrUpdate: createOrUpdate}}
}
func (target clusterAdapter) get(ctx context.Context, key client.ObjectKey, object client.Object) error {
	return target.client.Get(ctx, key, object)
}
func (target clusterAdapter) delete(ctx context.Context, object client.Object) error {
	return target.client.Delete(ctx, object)
}
func (target clusterAdapter) upsert(ctx context.Context, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return target.createOrUpdate(ctx, target.client, object, mutate)
}
func (target HostedCluster) deleteIfNeeded(ctx context.Context, object client.Object) (bool, error) {
	return k8sutil.DeleteIfNeeded(ctx, target.client, object)
}
func (target HostedCluster) updateStatus(ctx context.Context, object client.Object) error {
	return target.client.Status().Update(ctx, object)
}
