package kcm

import (
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileService(svc *corev1.Service, owner config.OwnerRef) error {
	owner.ApplyTo(svc)
	svc.Spec.Selector = kcmLabels()

	// Ensure labels propagate to endpoints so service monitors can select them
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range kcmLabels() {
		svc.Labels[k] = v
	}

	// Setting this to PreferDualStack will make the service to be created with IPv4 and IPv6 addresses if the management cluster is dual stack.
	IPFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack
	svc.Spec.IPFamilyPolicy = &IPFamilyPolicy

	svc.Spec.Type = corev1.ServiceTypeClusterIP

	if len(svc.Spec.Ports) == 0 {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name: "client",
			},
		}
	}

	svc.Spec.Ports[0].Port = DefaultPort
	svc.Spec.Ports[0].Name = "client"
	svc.Spec.Ports[0].TargetPort = intstr.FromString("client")
	svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP

	return nil
}
