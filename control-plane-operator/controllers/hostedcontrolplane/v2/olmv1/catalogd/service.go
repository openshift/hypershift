package catalogd

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func adaptService(cpContext component.WorkloadContext, svc *corev1.Service) error {
	// catalogd Service configuration for catalog content serving
	// Matches upstream catalogd-service definition from operator-controller helm chart

	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}

	// OpenShift service-ca annotation for automatic TLS certificate provisioning
	// service-ca operator provisions and rotates certificates automatically
	svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = "catalogserver-cert"

	if svc.Labels == nil {
		svc.Labels = make(map[string]string)
	}
	svc.Labels["app.kubernetes.io/name"] = ComponentName

	// Configure service ports matching upstream catalogd requirements
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
		{
			Name:       "webhook",
			Port:       9443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(9443),
		},
		{
			Name:       "metrics",
			Port:       7443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(7443),
		},
	}

	// Selector matches catalogd deployment pods
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = make(map[string]string)
	}
	svc.Spec.Selector["app.kubernetes.io/name"] = ComponentName

	return nil
}
