package configuration

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type configurationTestClients struct {
	client   client.Client
	cpClient client.Client
	upsert.CreateOrUpdateProvider
}

func (root configurationTestClients) hosted() HostedCluster {
	var createOrUpdate upsert.CreateOrUpdateFN
	if root.CreateOrUpdateProvider != nil {
		createOrUpdate = root.CreateOrUpdate
	}
	return NewHostedCluster(root.client, createOrUpdate)
}
func (root configurationTestClients) controlPlane() ControlPlane {
	var createOrUpdate upsert.CreateOrUpdateFN
	if root.CreateOrUpdateProvider != nil {
		createOrUpdate = root.CreateOrUpdate
	}
	return NewControlPlane(root.cpClient, createOrUpdate)
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
func globalParams(hcp *hyperv1.HostedControlPlane) GlobalConfigParams {
	return GlobalConfigParams{
		Name: hcp.Name, Namespace: hcp.Namespace, SwiftPodNetworkInstance: hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation],
		Endpoint: hcp.Status.ControlPlaneEndpoint, KubeAPIServerDNSName: hcp.Spec.KubeAPIServerDNSName,
		InfraID: hcp.Spec.InfraID, InfrastructureAvailabilityPolicy: hcp.Spec.InfrastructureAvailabilityPolicy,
		DNS: hcp.Spec.DNS, Platform: hcp.Spec.Platform, Networking: hcp.Spec.Networking,
		Configuration: hcp.Spec.Configuration, IssuerURL: hcp.Spec.IssuerURL, ImageContentSources: hcp.Spec.ImageContentSources,
	}
}
