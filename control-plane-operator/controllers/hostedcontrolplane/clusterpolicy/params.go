package clusterpolicy

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	DefaultPriorityClass = "system-node-critical"
)

type ClusterPolicyControllerParams struct {
	Image     string              `json:"image"`
	APIServer *configv1.APIServer `json:"apiServer"`

	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
	config.OwnerRef  `json:",inline"`
}

func NewClusterPolicyControllerParams(hcp *hyperv1.HostedControlPlane, globalConfig config.GlobalConfig, images map[string]string) *ClusterPolicyControllerParams {
	params := &ClusterPolicyControllerParams{
		Image:     images["cluster-policy-controller"],
		APIServer: globalConfig.APIServer,
	}
	params.DeploymentConfig = config.DeploymentConfig{
		AdditionalLabels: map[string]string{},
		Scheduling: config.Scheduling{
			PriorityClass: DefaultPriorityClass,
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

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.DeploymentConfig.Replicas = 3
	default:
		params.DeploymentConfig.Replicas = 1
	}
	params.OwnerRef = config.OwnerRefFrom(hcp)
	return params
}

func (p *ClusterPolicyControllerParams) MinTLSVersion() string {
	if p.APIServer != nil {
		return config.MinTLSVersion(p.APIServer.Spec.TLSSecurityProfile)
	} else {
		return config.MinTLSVersion(nil)
	}
}

func (p *ClusterPolicyControllerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.Spec.TLSSecurityProfile)
	} else {
		return config.CipherSuites(nil)
	}
}
