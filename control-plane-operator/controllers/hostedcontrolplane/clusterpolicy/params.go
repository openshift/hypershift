package clusterpolicy

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

type ClusterPolicyControllerParams struct {
	FeatureGate             *configv1.FeatureGateSpec `json:"featureGate"`
	Image                   string                    `json:"image"`
	APIServer               *configv1.APIServerSpec   `json:"apiServer"`
	AvailabilityProberImage string                    `json:"availabilityProberImage"`

	DeploymentConfig config.DeploymentConfig `json:"deploymentConfig"`
	config.OwnerRef  `json:",inline"`
}

func NewClusterPolicyControllerParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, setDefaultSecurityContext bool) *ClusterPolicyControllerParams {
	params := &ClusterPolicyControllerParams{
		Image:                   releaseImageProvider.GetImage("cluster-policy-controller"),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
	}
	if hcp.Spec.Configuration != nil {
		params.APIServer = hcp.Spec.Configuration.APIServer
		params.FeatureGate = hcp.Spec.Configuration.FeatureGate
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
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	replicas := ptr.To(2)
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		replicas = ptr.To(1)
	}
	params.DeploymentConfig.SetDefaults(hcp, clusterPolicyControllerLabels, replicas)
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

func (p *ClusterPolicyControllerParams) FeatureGates() []string {
	if p.FeatureGate != nil {
		return config.FeatureGates(p.FeatureGate.FeatureGateSelection)
	} else {
		return config.FeatureGates(configv1.FeatureGateSelection{
			FeatureSet: configv1.Default,
		})
	}
}

func (p *ClusterPolicyControllerParams) CipherSuites() []string {
	if p.APIServer != nil {
		return config.CipherSuites(p.APIServer.TLSSecurityProfile)
	} else {
		return config.CipherSuites(nil)
	}
}
