package oapi

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	OpenShiftAPIServerPort      = 8443
	OpenShiftOAuthAPIServerPort = 8443
	OpenShiftServicePort        = 443
	OLMPackageServerPort        = 5443
)

var (
	openshiftAPIServerLabels = map[string]string{"app": "openshift-apiserver"}
	oauthAPIServerLabels     = map[string]string{"app": "openshift-oauth-apiserver"}
	olmPackageServerLabels   = map[string]string{"app": "packageserver"}
)

func ReconcileOpenShiftAPIService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileAPIService(svc, ownerRef, openshiftAPIServerLabels, OpenShiftAPIServerPort)
}

func ReconcileOAuthAPIService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileAPIService(svc, ownerRef, oauthAPIServerLabels, OpenShiftAPIServerPort)
}

func ReconcileOLMPackageServerService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileAPIService(svc, ownerRef, olmPackageServerLabels, OLMPackageServerPort)
}

func reconcileAPIService(svc *corev1.Service, ownerRef config.OwnerRef, labels map[string]string, targetPort int) error {
	ownerRef.ApplyTo(svc)
	svc.Spec.Selector = labels
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Name = "https"
	portSpec.Port = int32(OpenShiftServicePort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(targetPort)
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileWorkerService(cm *corev1.ConfigMap, ownerRef config.OwnerRef, svc *corev1.Service) error {
	ownerRef.ApplyTo(cm)
	if err := reconcileClusterService(svc); err != nil {
		return err
	}
	ownerRef.ApplyTo(cm)
	util.ReconcileWorkerManifest(cm, svc)
	return nil
}

func reconcileClusterService(svc *corev1.Service) error {
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name: "https",
			Port: OpenShiftServicePort,
		},
	}
	return nil
}
