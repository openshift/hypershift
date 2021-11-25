package none

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type None struct{}

func (p None) ReconcileCAPIInfraCR(hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint,
	c client.Client, ctx context.Context) (client.Object, error) {

	return nil, nil
}

func (p None) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error) {
	return nil, nil
}

func (p None) ReconcileCredentials(c client.Client, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (None) ReconcileSecretEncryption(hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, ctx context.Context, c client.Client,
	createOrUpdate upsert.CreateOrUpdateFN) error {
	return nil
}
