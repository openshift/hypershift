package hostedcontrolplane

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultAPIServerPort = 6443
	vpnServicePort       = 1194
)

func OauthAPIServerServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
}

func OauthAPIServerServiceSelector() map[string]string {
	return map[string]string{"app": "openshift-oauth-apiserver"}
}

func OpenshiftAPIServerServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
}

func OpenshiftAPIServerServiceSelector() map[string]string {
	return map[string]string{"app": "openshift-apiserver"}
}

func VPNServerServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Port:       vpnServicePort,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(vpnServicePort),
		},
	}
}

func VPNServerServiceSelector() map[string]string {
	return map[string]string{"app": "openvpn-server"}
}

func OauthServerServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
}

func OauthServerServiceSelector() map[string]string {
	return map[string]string{"app": "oauth-openshift"}
}

func KubeAPIServerServicePorts(securePort int32) []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Port:       securePort,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(int(securePort)),
		},
	}
}

func KubeAPIServerServiceSelector() map[string]string {
	return map[string]string{"app": "kube-apiserver"}
}
