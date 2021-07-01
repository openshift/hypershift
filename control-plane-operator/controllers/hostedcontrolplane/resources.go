package hostedcontrolplane

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultAPIServerPort = 6443
)

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
