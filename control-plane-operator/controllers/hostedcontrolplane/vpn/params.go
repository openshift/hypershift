package vpn

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	DefaultPriorityClass = "system-node-critical"
)

type VPNParams struct {
	Network          configv1.Network           `json:"network"`
	MachineCIDR      string                     `json:"computeCIDR"`
	VPNImage         string                     `json:"vpnImage"`
	ExternalAddress  string                     `json:"externalAddress"`
	ExternalPort     int32                      `json:"externalPort"`
	SecurityContexts config.SecurityContextSpec `json:"securityContexts"`
	Resources        config.ResourcesSpec       `json:"resources"`
	ServerScheduling config.Scheduling          `json:"serverScheduling"`
	ClientScheduling config.Scheduling          `json:"clientScheduling"`
	OwnerReference   *metav1.OwnerReference     `json:"ownerReference"`
}

func NewVPNParams(hcp *hyperv1.HostedControlPlane, images map[string]string, externalAddress string, externalPort int32) *VPNParams {
	return &VPNParams{
		Network:         config.Network(hcp),
		MachineCIDR:     hcp.Spec.MachineCIDR,
		VPNImage:        images["vpn"],
		ExternalAddress: externalAddress,
		ExternalPort:    externalPort,
		SecurityContexts: config.SecurityContextSpec{
			vpnContainerServer().Name: {
				Privileged: pointer.BoolPtr(true),
			},
			vpnContainerClient().Name: {
				Privileged: pointer.BoolPtr(true),
			},
		},
		Resources: config.ResourcesSpec{
			vpnContainerServer().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
			},
		},
		ServerScheduling: config.Scheduling{
			PriorityClass: DefaultPriorityClass,
		},
		OwnerReference: config.ControllerOwnerRef(hcp),
	}
}
