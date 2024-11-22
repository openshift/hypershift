package ingress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
)

func ReconcileDefaultIngressController(ingressController *operatorv1.IngressController, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32, isIBMCloudUPI bool, isPrivate bool, useNLB bool, loadBalancerScope operatorv1.LoadBalancerScope, loadBalancerIP string) error {
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
	case hyperv1.AWSPlatform:
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
					Scope: loadBalancerScope,
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
	case hyperv1.OpenStackPlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
			LoadBalancer: &operatorv1.LoadBalancerStrategy{
				Scope: loadBalancerScope,
				ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
					Type: operatorv1.OpenStackLoadBalancerProvider,
					// TODO(emilien): add the field once bumped openshift/api and also remove `ReconcileDefaultIngressControllerWithUnstructured`.
					// https://github.com/openshift/hypershift/pull/4927
					// OpenStack: &operatorv1.OpenStackLoadBalancerParameters{
					// 	FloatingIP: loadBalancerIP,
					// },
				},
			},
		}
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
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

// ReconcileDefaultIngressControllerWithUnstructured reconciles the default ingress controller with an unstructured object
// that has custom fields set per platform.
func ReconcileOpenStackDefaultIngressController(ingressController *unstructured.Unstructured, ingressSubdomain string, replicas int32, isPrivate bool, loadBalancerScope operatorv1.LoadBalancerScope, loadBalancerIP string) error {
	// If ingress controller already exists, skip reconciliation to allow day-2 configuration
	if ingressController.GetResourceVersion() != "" {
		return nil
	}

	unstructured.SetNestedField(ingressController.Object, ingressSubdomain, "spec", "domain")
	unstructured.SetNestedField(ingressController.Object, string(operatorv1.LoadBalancerServiceStrategyType), "spec", "endpointPublishingStrategy", "type")
	unstructured.SetNestedField(ingressController.Object, int64(replicas), "spec", "replicas")

	unstructured.SetNestedField(ingressController.Object, string(loadBalancerScope), "spec", "endpointPublishingStrategy", "loadBalancer", "scope")
	unstructured.SetNestedField(ingressController.Object, string(operatorv1.OpenStackLoadBalancerProvider), "spec", "endpointPublishingStrategy", "loadBalancer", "providerParameters", "type")
	if loadBalancerIP != "" {
		unstructured.SetNestedField(ingressController.Object, loadBalancerIP, "spec", "endpointPublishingStrategy", "loadBalancer", "providerParameters", "openstack", "floatingIP")
	}
	unstructured.SetNestedField(ingressController.Object, manifests.IngressDefaultIngressControllerCert().Name, "spec", "defaultCertificate", "name")

	if isPrivate {
		unstructured.SetNestedField(ingressController.Object, operatorv1.PrivateStrategyType, "spec", "endpointPublishingStrategy", "type")
		unstructured.SetNestedMap(ingressController.Object, map[string]interface{}{}, "spec", "endpointPublishingStrategy", "private")
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
