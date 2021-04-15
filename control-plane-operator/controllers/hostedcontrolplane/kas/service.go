package kas

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (p *KubeAPIServerParams) ReconcileService(svc *corev1.Service) error {
	if len(svc.Spec.Ports) > 0 {
		svc.Spec.Ports[0].Port = p.APIServerPort
		svc.Spec.Ports[0].TargetPort = intstr.FromInt(int(p.APIServerPort))
	} else {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Port:       p.APIServerPort,
				TargetPort: intstr.FromInt(int(p.APIServerPort)),
			},
		}
	}
	svc.Spec.Selector = kasLabels
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	return nil
}
