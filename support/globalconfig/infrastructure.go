package globalconfig

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func InfrastructureConfig() *configv1.Infrastructure {
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	return infra
}

func ReconcileInfrastructure(infra *configv1.Infrastructure, hcp *hyperv1.HostedControlPlane) {

	platformType := hcp.Spec.Platform.Type
	switch hcp.Spec.Platform.Type {
	// In case of KubeVirtPlatform, set the Infrastructure to NonePlatform
	// This is done in order to prevent error in machine-config-server
	case hyperv1.KubevirtPlatform:
		platformType = hyperv1.NonePlatform
	}

	apiServerAddress := hcp.Status.ControlPlaneEndpoint.Host
	apiServerPort := hcp.Status.ControlPlaneEndpoint.Port

	infra.Spec.PlatformSpec.Type = configv1.PlatformType(platformType)
	infra.Status.APIServerInternalURL = fmt.Sprintf("https://%s:%d", apiServerAddress, apiServerPort)
	infra.Status.APIServerURL = fmt.Sprintf("https://%s:%d", apiServerAddress, apiServerPort)
	infra.Status.EtcdDiscoveryDomain = BaseDomain(hcp)
	infra.Status.InfrastructureName = hcp.Spec.InfraID
	infra.Status.ControlPlaneTopology = configv1.ExternalTopologyMode
	infra.Status.Platform = configv1.PlatformType(platformType)
	infra.Status.PlatformStatus = &configv1.PlatformStatus{
		Type: configv1.PlatformType(platformType),
	}

	switch hcp.Spec.InfrastructureAvailabilityPolicy {
	case hyperv1.SingleReplica:
		infra.Status.InfrastructureTopology = configv1.SingleReplicaTopologyMode
	default:
		infra.Status.InfrastructureTopology = configv1.HighlyAvailableTopologyMode
	}

	switch platformType {
	case hyperv1.AWSPlatform:
		infra.Spec.PlatformSpec.AWS = &configv1.AWSPlatformSpec{}
		infra.Status.PlatformStatus.AWS = &configv1.AWSPlatformStatus{
			Region: hcp.Spec.Platform.AWS.Region,
		}
		tags := []configv1.AWSResourceTag{}
		for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
			// This breaks the AWS CSI driver as it ends up being used there as an extra tag
			// which makes it fail to start with "Invalid extra tags: Tag key prefix 'kubernetes.io' is reserved".
			if strings.HasPrefix(tag.Key, "kubernetes.io") {
				continue
			}
			tags = append(tags, configv1.AWSResourceTag{
				Key:   tag.Key,
				Value: tag.Value,
			})
		}
		infra.Status.PlatformStatus.AWS.ResourceTags = tags
	case hyperv1.AzurePlatform:
		infra.Spec.CloudConfig.Name = "cloud.conf"
		infra.Status.PlatformStatus.Azure = &configv1.AzurePlatformStatus{
			CloudName:         configv1.AzurePublicCloud,
			ResourceGroupName: hcp.Spec.Platform.Azure.ResourceGroupName,
		}
	}
}
