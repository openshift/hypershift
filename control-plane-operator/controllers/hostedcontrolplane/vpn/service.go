package vpn

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	VPNServerPort = 1194
)

func (p *VPNServiceParams) ReconcileService(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy) error {
	util.EnsureOwnerRef(svc, p.OwnerReference)
	svc.Spec.Selector = vpnServerLabels
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(VPNServerPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(VPNServerPort)
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	default:
		return fmt.Errorf("invalid publishing strategy for VPN service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func (p *VPNServiceParams) ReconcileServiceStatus(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy) (host string, port int32, err error) {
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return
		}
		switch {
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
			port = int32(VPNServerPort)
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			host = svc.Status.LoadBalancer.Ingress[0].IP
			port = int32(VPNServerPort)
		}
	case hyperv1.NodePort:
		if strategy.NodePort == nil {
			err = fmt.Errorf("strategy details not specified for VPN nodeport type service")
			return
		}
		if len(svc.Spec.Ports) == 0 {
			return
		}
		if svc.Spec.Ports[0].NodePort == 0 {
			return
		}
		port = svc.Spec.Ports[0].NodePort
		host = strategy.NodePort.Address
	}
	return

}
