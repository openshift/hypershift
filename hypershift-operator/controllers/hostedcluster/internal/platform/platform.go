package platform

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/agent"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/aws"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/azure"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/ibmcloud"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/none"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/openstack"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/powervs"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	imgUtil "github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	AWSCAPIProvider             = "aws-cluster-api-controllers"
	AzureCAPIProvider           = "azure-cluster-api-controllers"
	PowerVSCAPIProvider         = "ibmcloud-cluster-api-controllers"
	OpenStackCAPIProvider       = "openstack-cluster-api-controllers"
	OpenStackResourceController = "openstack-resource-controller"
)

var _ Platform = aws.AWS{}
var _ Platform = azure.Azure{}
var _ Platform = ibmcloud.IBMCloud{}
var _ Platform = none.None{}
var _ Platform = agent.Agent{}
var _ Platform = kubevirt.Kubevirt{}

type Platform interface {
	// ReconcileCAPIInfraCR is called during HostedCluster reconciliation prior to reconciling the CAPI Cluster CR.
	// Implementations should use the given input and client to create and update the desired state of the
	// platform infrastructure CAPI CR, which will then be referenced by the CAPI Cluster CR.
	// TODO (alberto): Pass createOrUpdate construct instead of client.
	ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error)

	// CAPIProviderDeploymentSpec is called during HostedCluster reconciliation prior to reconciling
	// the CAPI provider Deployment.
	// It should return a CAPI provider DeploymentSpec with the specific needs for a particular platform.
	// E.g particular volumes and secrets for credentials, containers, etc.
	CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error)

	// ReconcileCredentials is responsible for reconciling resources related to cloud credentials
	// from the HostedCluster namespace into to the HostedControlPlaneNamespace. So they can be used by
	// the Control Plane Operator.
	ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error

	// ReconcileSecretEncryption is responsible for reconciling resources related to secret encryption
	// from the HostedCluster namespace into to the HostedControlPlaneNamespace. So they can be used by
	// the Control Plane Operator if your platform supports KMS.
	ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error

	// CAPIProviderPolicyRules responsible to return list of policy rules are required to be used
	// by the CAPI provider in order to manage the resources by this platform
	// Return nil if no additional policy rule is required
	CAPIProviderPolicyRules() []rbacv1.PolicyRule

	// DeleteCredentials is responsible for deleting resources related to platform credentials
	// So they won't leak on upon hostedcluster deletion
	DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error
}

// OrphanDeleter is an interface implemented by providers for which it is possible to determine if machines have
// been orphaned by a failure to communicate with the provider.
type OrphanDeleter interface {
	// DeleteOrphanedMachines removes the finalizer from provider machines if they have been deleted and it is no
	// longer possible to delete them normally via the provider (ie. the OIDC provider is no longer valid)
	DeleteOrphanedMachines(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, controlPlaneNamespace string) error
}

// GetPlatform gets and initializes the cloud platform the hosted cluster was created on
func GetPlatform(ctx context.Context, hcluster *hyperv1.HostedCluster, releaseProvider releaseinfo.Provider, utilitiesImage string, pullSecretBytes []byte) (Platform, error) {
	var (
		platform          Platform
		capiImageProvider string
		orcImage          string
		payloadVersion    *semver.Version
		err               error
	)

	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if pullSecretBytes != nil {
			capiImageProvider, err = imgUtil.GetPayloadImage(ctx, releaseProvider, hcluster, AWSCAPIProvider, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve capi image: %w", err)
			}
			payloadVersion, err = imgUtil.GetPayloadVersion(ctx, releaseProvider, hcluster, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch payload version: %w", err)
			}
		}
		platform = aws.New(utilitiesImage, capiImageProvider, payloadVersion)
	case hyperv1.IBMCloudPlatform:
		platform = &ibmcloud.IBMCloud{}
	case hyperv1.NonePlatform:
		platform = &none.None{}
	case hyperv1.AgentPlatform:
		platform = &agent.Agent{}
	case hyperv1.KubevirtPlatform:
		platform = &kubevirt.Kubevirt{}
	case hyperv1.AzurePlatform:
		if pullSecretBytes != nil {
			capiImageProvider, err = imgUtil.GetPayloadImage(ctx, releaseProvider, hcluster, AzureCAPIProvider, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve capi image: %w", err)
			}
		}
		platform = azure.New(capiImageProvider)
	case hyperv1.PowerVSPlatform:
		if pullSecretBytes != nil {
			capiImageProvider, err = imgUtil.GetPayloadImage(ctx, releaseProvider, hcluster, PowerVSCAPIProvider, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve capi image: %w", err)
			}
		}
		platform = powervs.New(capiImageProvider)
	case hyperv1.OpenStackPlatform:
		if pullSecretBytes != nil {
			capiImageProvider, err = imgUtil.GetPayloadImage(ctx, releaseProvider, hcluster, OpenStackCAPIProvider, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve capi image: %w", err)
			}
			orcImage, err = imgUtil.GetPayloadImage(ctx, releaseProvider, hcluster, OpenStackResourceController, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve orc image: %w", err)
			}
			payloadVersion, err = imgUtil.GetPayloadVersion(ctx, releaseProvider, hcluster, pullSecretBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch payload version: %w", err)
			}
		}
		platform = openstack.New(capiImageProvider, orcImage, payloadVersion)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", hcluster.Spec.Platform.Type)
	}
	return platform, nil
}
