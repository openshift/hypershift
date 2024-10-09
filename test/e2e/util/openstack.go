package util

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type OpenStackInfra struct {
	ctx                   context.Context
	mgmtClient            crclient.Client
	hostedCluster         *hyperv1.HostedCluster
	additionalNetworkName string
}

func NewOpenStackInfra(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) OpenStackInfra {
	return OpenStackInfra{
		ctx:           ctx,
		mgmtClient:    mgmtClient,
		hostedCluster: hostedCluster,
		// For now we hardcode the network name, but we should make it configurable
		// and also use Gophercloud to create a network with a dynamic name.
		additionalNetworkName: "hcp-nodepool-multinet-e2e",
	}
}

func (o OpenStackInfra) Namespace() string {
	creds := o.hostedCluster.Spec.Platform.Kubevirt.Credentials
	if creds != nil {
		return creds.InfraNamespace
	}
	return manifests.HostedControlPlaneNamespace(o.hostedCluster.Namespace, o.hostedCluster.Name)
}

func (o OpenStackInfra) AdditionalNetworkName() string {
	return o.additionalNetworkName
}

func (o OpenStackInfra) MGMTClient() crclient.Client {
	return o.mgmtClient
}

func (o OpenStackInfra) Ctx() context.Context {
	return o.ctx
}

func (o OpenStackInfra) HostedCluster() *hyperv1.HostedCluster {
	return o.hostedCluster
}
