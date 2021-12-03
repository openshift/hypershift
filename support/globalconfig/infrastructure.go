package globalconfig

import (
	"fmt"

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

	apiServerAddress := hcp.Status.ControlPlaneEndpoint.Host
	apiServerPort := hcp.Status.ControlPlaneEndpoint.Port

	infra.Spec.PlatformSpec.Type = configv1.PlatformType(hcp.Spec.Platform.Type)
	infra.Status.APIServerInternalURL = fmt.Sprintf("https://%s:%d", apiServerAddress, apiServerPort)
	infra.Status.APIServerURL = fmt.Sprintf("https://%s:%d", apiServerAddress, apiServerPort)
	infra.Status.EtcdDiscoveryDomain = BaseDomain(hcp)
	infra.Status.InfrastructureName = hcp.Spec.InfraID
	infra.Status.Platform = configv1.PlatformType(hcp.Spec.Platform.Type)

	switch hcp.Spec.InfrastructureAvailabilityPolicy {
	case hyperv1.SingleReplica:
		infra.Status.InfrastructureTopology = configv1.SingleReplicaTopologyMode
		infra.Status.ControlPlaneTopology = configv1.SingleReplicaTopologyMode
	default:
		infra.Status.InfrastructureTopology = configv1.HighlyAvailableTopologyMode
		infra.Status.ControlPlaneTopology = configv1.HighlyAvailableTopologyMode
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		infra.Spec.PlatformSpec.AWS = &configv1.AWSPlatformSpec{}
		infra.Status.PlatformStatus = &configv1.PlatformStatus{}
		infra.Status.PlatformStatus.Type = configv1.AWSPlatformType
		infra.Status.PlatformStatus.AWS = &configv1.AWSPlatformStatus{
			Region: hcp.Spec.Platform.AWS.Region,
		}
		tags := []configv1.AWSResourceTag{}
		for _, tag := range hcp.Spec.Platform.AWS.ResourceTags {
			tags = append(tags, configv1.AWSResourceTag{
				Key:   tag.Key,
				Value: tag.Value,
			})
		}
		infra.Status.PlatformStatus.AWS.ResourceTags = tags
	case hyperv1.IBMCloudPlatform:
		infra.Status.PlatformStatus = &configv1.PlatformStatus{}
		infra.Status.PlatformStatus.Type = configv1.IBMCloudPlatformType
	}
}
