package platform

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/agent"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/ibmcloud"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/none"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Platform = aws.AWS{}
var _ Platform = ibmcloud.IBMCloud{}
var _ Platform = none.None{}
var _ Platform = agent.Agent{}

//go:generate mockgen -source=./platform.go -destination=./mock/platform_generated.go -package=mock
type Platform interface {
	// ReconcileCAPIInfraCR is called during HostedCluster reconciliation prior to reconciling the CAPI Cluster CR.
	// Implementations should use the given input and client to create and update the desired state of the
	// platform infrastructure CAPI CR, which will then be referenced by the CAPI Cluster CR.
	// TODO (alberto): Pass createOrUpdate construct instead of client.
	ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
		hcluster *hyperv1.HostedCluster,
		controlPlaneNamespace string,
		apiEndpoint hyperv1.APIEndpoint,
	) (client.Object, error)

	// CAPIProviderDeploymentSpec is called during HostedCluster reconciliation prior to reconciling
	// the CAPI provider Deployment.
	// It should return a CAPI provider DeploymentSpec with the specific needs for a particular platform.
	// E.g particular volumes and secrets for credentials, containers, etc.
	CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error)

	// ReconcileCredentials is responsible for reconciling resources related to cloud credentials
	// from the HostedCluster namespace into to the HostedControlPlaneNamespace. So they can be used by
	// the Control Plane Operator.
	ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
		hcluster *hyperv1.HostedCluster,
		controlPlaneNamespace string) error

	// ReconcileSecretEncryption is responsible for reconciling resources related to secret encryption
	// from the HostedCluster namespace into to the HostedControlPlaneNamespace. So they can be used by
	// the Control Plane Operator if your platform supports KMS.
	ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
		hcluster *hyperv1.HostedCluster,
		controlPlaneNamespace string) error

	// CAPIProviderPolicyRules responsible to return list of policy rules are required to be used
	// by the CAPI provider in order to manage the resources by this platform
	// Return nil if no aditional policy rule is required
	CAPIProviderPolicyRules() []rbacv1.PolicyRule
}

func GetPlatform(hcluster *hyperv1.HostedCluster) (Platform, error) {
	var platform Platform
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		platform = &aws.AWS{}
	case hyperv1.IBMCloudPlatform:
		platform = &ibmcloud.IBMCloud{}
	case hyperv1.NonePlatform:
		platform = &none.None{}
	case hyperv1.AgentPlatform:
		platform = &agent.Agent{}
	case hyperv1.KubevirtPlatform:
		platform = &kubevirt.Kubevirt{}
	default:
		return nil, fmt.Errorf("unsupported platform: %s", hcluster.Spec.Platform.Type)
	}
	return platform, nil
}
