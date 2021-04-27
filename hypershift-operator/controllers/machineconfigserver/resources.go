package machineconfigserver

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func MachineConfigServerServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{
			Name:       "https",
			Protocol:   corev1.ProtocolTCP,
			Port:       443,
			TargetPort: intstr.FromInt(8081),
		},
	}
}

func MachineConfigServerServiceSelector(machineConfigServerName string) map[string]string {
	return map[string]string{
		"app": fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
	}
}
