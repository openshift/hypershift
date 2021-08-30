package ingress

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileDefaultIngressControllerWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32) error {
	ownerRef.ApplyTo(cm)
	ingressController := manifests.IngressDefaultIngressController()
	if err := reconcileDefaultIngressController(ingressController, ingressSubdomain, platformType, replicas); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, ingressController)
}

func ReconcileDefaultIngressControllerCertWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, ingressCert *corev1.Secret) error {
	ownerRef.ApplyTo(cm)
	certSecret := manifests.IngressDefaultIngressControllerCert()
	if err := reconcileDefaultIngressControllerCertSecret(certSecret, ingressCert); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, certSecret)
}

func reconcileDefaultIngressController(ingressController *operatorv1.IngressController, ingressSubdomain string, platformType hyperv1.PlatformType, replicas int32) error {
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

func reconcileDefaultIngressControllerCertSecret(certSecret *corev1.Secret, sourceSecret *corev1.Secret) error {
	if _, hasCertKey := sourceSecret.Data[corev1.TLSCertKey]; !hasCertKey {
		return fmt.Errorf("source secret %s/%s does not have a cert key", sourceSecret.Namespace, sourceSecret.Name)
	}
	if _, hasKeyKey := sourceSecret.Data[corev1.TLSPrivateKeyKey]; !hasKeyKey {
		return fmt.Errorf("source secret %s/%s does not have a key key", sourceSecret.Namespace, sourceSecret.Name)
	}

	if certSecret.Data == nil {
		certSecret.Data = map[string][]byte{}
	}
	certSecret.Data[corev1.TLSCertKey] = sourceSecret.Data[corev1.TLSCertKey]
	certSecret.Data[corev1.TLSPrivateKeyKey] = sourceSecret.Data[corev1.TLSPrivateKeyKey]
	return nil
}
