package configoperator

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

type HostedClusterConfigOperatorParams struct {
	config.DeploymentConfig
	config.OwnerRef
	Image                    string
	OpenShiftVersion         string
	KubernetesVersion        string
	AvailabilityProberImage  string
	HAProxyImage             string
	APIServerExternalAddress string
	APIServerExternalPort    int32
	APIServerInternalAddress string
	APIServerInternalPort    int32
}

func NewHostedClusterConfigOperatorParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, images map[string]string, openShiftVersion, kubernetesVersion string, apiServerAddress string, apiServerPort int32) *HostedClusterConfigOperatorParams {
	params := &HostedClusterConfigOperatorParams{
		Image:                    images["hosted-cluster-config-operator"],
		OwnerRef:                 config.OwnerRefFrom(hcp),
		OpenShiftVersion:         openShiftVersion,
		KubernetesVersion:        kubernetesVersion,
		AvailabilityProberImage:  images[util.AvailabilityProberImageName],
		HAProxyImage:             images["haproxy-router"],
		APIServerExternalAddress: apiServerAddress,
		APIServerExternalPort:    apiServerPort,
	}
	params.Replicas = 1
	params.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.Resources = map[string]corev1.ResourceRequirements{
		hccContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("60m"),
			},
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)

	if hcp.Spec.APIAdvertiseAddress != nil {
		params.APIServerInternalAddress = *hcp.Spec.APIAdvertiseAddress
	} else {
		params.APIServerInternalAddress = config.DefaultAdvertiseAddress
	}
	if hcp.Spec.APIPort != nil {
		params.APIServerInternalPort = *hcp.Spec.APIPort
	} else {
		params.APIServerInternalPort = config.DefaultAPIServerPort
	}
	return params
}
