package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/podspec"
)

const (
	KubeconfigKey = podspec.KubeconfigKey
)

func InClusterKASURL(platformType hyperv1.PlatformType) string {
	if platformType == hyperv1.IBMCloudPlatform {
		return fmt.Sprintf("https://%s:%d", cpomanifests.KubeAPIServerServiceName, config.KASSVCIBMCloudPort)
	}
	return fmt.Sprintf("https://%s:%d", cpomanifests.KubeAPIServerServiceName, config.KASSVCPort)
}

func InClusterKASReadyURL(platformType hyperv1.PlatformType) string {
	return InClusterKASURL(platformType) + "/readyz"
}
