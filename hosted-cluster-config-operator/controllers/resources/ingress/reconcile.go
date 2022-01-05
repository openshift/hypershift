package ingress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/controllers/resources/manifests"
)

func ReconcileDefaultIngressController(ingressController *operatorv1.IngressController, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32) error {
	ingressController.Spec.Domain = ingressSubdomain
	ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
		Type: operatorv1.LoadBalancerServiceStrategyType,
	}
	if replicas > 0 {
		ingressController.Spec.Replicas = &(replicas)
	}
	switch platformType {
	case hyperv1.NonePlatform, hyperv1.KubevirtPlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.HostNetworkStrategyType,
		}
		ingressController.Spec.DefaultCertificate = &corev1.LocalObjectReference{
			Name: manifests.IngressDefaultIngressControllerCert().Name,
		}
	case hyperv1.IBMCloudPlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
			LoadBalancer: &operatorv1.LoadBalancerStrategy{
				Scope: operatorv1.ExternalLoadBalancer,
			},
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
