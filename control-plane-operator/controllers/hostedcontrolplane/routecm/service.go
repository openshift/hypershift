package routecm

import (
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Labels = openShiftRouteControllerManagerLabels()
	svc.Spec.Selector = openShiftRouteControllerManagerLabels()
	// Setting this to PreferDualStack will make the service to be created with IPv4 and IPv6 addresses if the management cluster is dual stack.
	IPFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack
	svc.Spec.IPFamilyPolicy = &IPFamilyPolicy
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Name = "https"
	portSpec.Port = servingPort
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromString("https")
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports[0] = portSpec
	return nil
}
