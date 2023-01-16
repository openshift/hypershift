package none

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type None struct{}

func (p None) ReconcileCAPIInfraCR(_ context.Context, _ client.Client, _ upsert.CreateOrUpdateFN,
	_ *hyperv1.HostedCluster,
	_ string, _ hyperv1.APIEndpoint) (client.Object, error) {

	return nil, nil
}

func (p None) CAPIProviderDeploymentSpec(_ *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	return nil, nil
}

func (p None) ReconcileCredentials(_ context.Context, _ client.Client, _ upsert.CreateOrUpdateFN,
	_ *hyperv1.HostedCluster,
	_ string) error {
	return nil
}

func (None) ReconcileSecretEncryption(_ context.Context, _ client.Client, _ upsert.CreateOrUpdateFN,
	_ *hyperv1.HostedCluster,
	_ string) error {
	return nil
}

func (None) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}

func (None) DeleteCredentials(_ context.Context, _ client.Client, _ *hyperv1.HostedCluster, _ string) error {
	return nil
}
