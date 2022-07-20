package clusterpolicy

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type ClusterPolicyControllerParams struct {
	Image                   string                  `json:"image"`
	APIServer               *configv1.APIServerSpec `json:"apiServer"`
	AvailabilityProberImage string                  `json:"availabilityProberImage"`

	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
	config.OwnerRef  `json:",inline"`
}

func NewClusterPolicyControllerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, setDefaultSecurityContext bool) *ClusterPolicyControllerParams {
	params := &ClusterPolicyControllerParams{
		Image:                   images["cluster-policy-controller"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
	}
	if hcp.Spec.Configuration != nil {
		params.APIServer = hcp.Spec.Configuration.APIServer
	}
	params.DeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
		Resources: map[string]corev1.ResourceRequirements{
			cpcContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
					corev1.ResourceCPU:    resource.MustParse("10m"),
				},
			},
		},
	}

	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetDefaults(hcp, clusterPolicyControllerLabels, nil)
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.OwnerRef = config.OwnerRefFrom(hcp)

	return params
}

func (p *ClusterPolicyControllerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.TLSSecurityProfile)
	} else {
		return config.MinTLSVersion(nil)
	}
}

func (p *ClusterPolicyControllerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	} else {
		return config.CipherSuites(nil)
	}
}
