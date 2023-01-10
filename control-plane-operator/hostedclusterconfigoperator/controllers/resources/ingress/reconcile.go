package ingress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
)

func ReconcileDefaultIngressController(ingressController *operatorv1.IngressController, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32, isIBMCloudUPI bool, isPrivate bool) error {
	// If ingress controller already exists, skip reconciliation to allow day-2 configuration
	if ingressController.ResourceVersion != "" {
		return nil
	}

	ingressController.Spec.Domain = ingressSubdomain
	ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
		Type: operatorv1.LoadBalancerServiceStrategyType,
	}
	if replicas > 0 {
		ingressController.Spec.Replicas = &(replicas)
	}
	switch platformType {
	case hyperv1.NonePlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.HostNetworkStrategyType,
		}
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
		}
	case hyperv1.KubevirtPlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.NodePortServiceStrategyType,
		}
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
		}
	case hyperv1.IBMCloudPlatform:
		if isIBMCloudUPI {
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.NodePortServiceStrategyType,
				NodePort: &operatorv1.NodePortStrategy{
					Protocol: operatorv1.TCPProtocol,
				},
			}
		} else {
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: operatorv1.ExternalLoadBalancer,
				},
			}
		}
		ingressController.Spec.NodePlacement = &operatorv1.NodePlacement{
			Tolerations: []corev1.Toleration{
				{
					Key:   "dedicated",
					Value: "edge",
				},
			},
		}
	default:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
		}
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
		}
	}
	if isPrivate {
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type:    operatorv1.PrivateStrategyType,
			Private: &operatorv1.PrivateStrategy{},
		}
	}
	return nil
}

func ReconcileDefaultIngressControllerCertSecret(certSecret *corev1.Secret, sourceSecret *corev1.Secret) error {
	if _, hasCertKey := sourceSecret.Data[corev1.TLSCertKey]; !hasCertKey {
		return fmt.Errorf("source secret %s/%s does not have a cert key", sourceSecret.Namespace, sourceSecret.Name)
	}
	if _, hasKeyKey := sourceSecret.Data[corev1.TLSPrivateKeyKey]; !hasKeyKey {
		return fmt.Errorf("source secret %s/%s does not have a key key", sourceSecret.Namespace, sourceSecret.Name)
	}

	certSecret.Data = map[string][]byte{}
	certSecret.Data[corev1.TLSCertKey] = sourceSecret.Data[corev1.TLSCertKey]
	certSecret.Data[corev1.TLSPrivateKeyKey] = sourceSecret.Data[corev1.TLSPrivateKeyKey]
	return nil
}

func ReconcileDefaultIngressPassthroughService(service *corev1.Service, defaultNodePort *corev1.Service, hcp *hyperv1.HostedControlPlane) error {
	detectedHTTPSNodePort := int32(0)

	for _, port := range defaultNodePort.Spec.Ports {
		if port.Port == 443 {
			detectedHTTPSNodePort = port.NodePort
			break
		}
	}

	if detectedHTTPSNodePort == 0 {
		return fmt.Errorf("unable to detect default ingress NodePort https port")
	}

	if service.Labels == nil {
		service.Labels = map[string]string{}
	}
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https-443",
			Protocol:   corev1.ProtocolTCP,
			Port:       443,
			TargetPort: intstr.FromInt(int(detectedHTTPSNodePort)),
		},
	}
	service.Spec.Selector = map[string]string{
		"kubevirt.io":        "virt-launcher",
		hyperv1.InfraIDLabel: hcp.Spec.InfraID,
	}
	service.Spec.Type = corev1.ServiceTypeClusterIP
	service.Labels[hyperv1.InfraIDLabel] = hcp.Spec.InfraID

	return nil
}

func ReconcileDefaultIngressPassthroughRoute(route *routev1.Route, cpService *corev1.Service, hcp *hyperv1.HostedControlPlane) error {
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Spec.WildcardPolicy = routev1.WildcardPolicySubdomain
	route.Spec.Host = fmt.Sprintf("https.apps.%s.%s", hcp.Name, hcp.Spec.DNS.BaseDomain)
	route.Spec.TLS = &routev1.TLSConfig{
		Termination: routev1.TLSTerminationPassthrough,
	}
	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: cpService.Name,
	}
	route.Labels[hyperv1.InfraIDLabel] = hcp.Spec.InfraID

	return nil
}
