package ingress

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcileDefaultIngressController(ingressController *operatorv1.IngressController, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32, isIBMCloudUPI bool, isPrivate bool, useNLB bool, loadBalancerScope operatorv1.LoadBalancerScope, loadBalancerIP string, endpointPublishingStrategy *operatorv1.EndpointPublishingStrategy) error {
	// If ingress controller already exists, skip reconciliation to allow day-2 configuration
	if ingressController.ResourceVersion != "" {
		return nil
	}

	ingressController.Spec.Domain = ingressSubdomain
	if replicas > 0 {
		ingressController.Spec.Replicas = &(replicas)
	}

	// If endpointPublishingStrategy is provided via configuration, use it directly
	if endpointPublishingStrategy != nil {
		ingressController.Spec.EndpointPublishingStrategy = endpointPublishingStrategy
	} else {
		// Otherwise, use platform-specific defaults
		switch platformType {
		case hyperv1.NonePlatform:
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.HostNetworkStrategyType,
			}
		case hyperv1.KubevirtPlatform:
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.NodePortServiceStrategyType,
			}
		case hyperv1.AWSPlatform:
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
			}
			if useNLB {
				ingressController.Spec.EndpointPublishingStrategy.LoadBalancer = &operatorv1.LoadBalancerStrategy{
					Scope: loadBalancerScope,
					ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
						Type: operatorv1.AWSLoadBalancerProvider,
						AWS: &operatorv1.AWSLoadBalancerParameters{
							Type:                          operatorv1.AWSNetworkLoadBalancer,
							NetworkLoadBalancerParameters: &operatorv1.AWSNetworkLoadBalancerParameters{},
						},
					},
				}
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
						Scope: loadBalancerScope,
					},
				}
			}
		case hyperv1.OpenStackPlatform:
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
				LoadBalancer: &operatorv1.LoadBalancerStrategy{
					Scope: loadBalancerScope,
					ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
						Type: operatorv1.OpenStackLoadBalancerProvider,
						OpenStack: &operatorv1.OpenStackLoadBalancerParameters{
							FloatingIP: loadBalancerIP,
						},
					},
				},
			}
		default:
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type: operatorv1.LoadBalancerServiceStrategyType,
			}
		}

		// Override with Private strategy if isPrivate annotation is set
		if isPrivate {
			ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
				Type:    operatorv1.PrivateStrategyType,
				Private: &operatorv1.PrivateStrategy{},
			}
		}
	}

	// Set default certificate for platforms that need it
	if platformType != hyperv1.IBMCloudPlatform {
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
		}
	}

	// Set node placement for IBM Cloud
	if platformType == hyperv1.IBMCloudPlatform {
		ingressController.Spec.NodePlacement = &operatorv1.NodePlacement{
			Tolerations: []corev1.Toleration{
				{
					Key:   "dedicated",
					Value: "edge",
				},
			},
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

	// The endpoints reconciliation is done at nodepool controller to support
	// secondary networks.
	service.Spec.Selector = map[string]string{}

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
