package configuration

import (
	"context"

	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type configurationTestClients struct {
	client   client.Client
	cpClient client.Client
	upsert.CreateOrUpdateProvider
}

func (root configurationTestClients) createOrUpdateFn() upsert.CreateOrUpdateFN {
	if root.CreateOrUpdateProvider != nil {
		return root.CreateOrUpdate
	}
	return nil
}
func (root configurationTestClients) hosted() HostedCluster {
	return NewHostedCluster(root.client, root.createOrUpdateFn())
}
func (root configurationTestClients) controlPlane() ControlPlane {
	return NewControlPlane(root.cpClient, root.createOrUpdateFn())
}

type configurationUpserter struct{ upsert.CreateOrUpdateFN }

func (provider configurationUpserter) CreateOrUpdate(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return provider.CreateOrUpdateFN(ctx, target, object, mutate)
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
}

type errorCreateOrUpdater struct{ err error }

func (provider *errorCreateOrUpdater) CreateOrUpdate(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.OperationResultNone, provider.err
}
