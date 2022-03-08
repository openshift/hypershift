package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileService(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy, owner *metav1.OwnerReference, apiServerPort int, isPublic bool) error {
	util.EnsureOwnerRef(svc, owner)
	svc.Spec.Selector = kasLabels()

	// Ensure labels propagate to endpoints so service
	// monitors can select them
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range kasLabels() {
		svc.Labels[k] = v
	}

	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(apiServerPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(apiServerPort)
	svc.ObjectMeta.Annotations = map[string]string{
		"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
	}
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if isPublic {
			svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		} else {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	default:
		return fmt.Errorf("invalid publishing strategy for Kube API server service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileServiceStatus(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy, apiServerPort int) (host string, port int32, err error) {
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return
		}
		switch {
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
			port = int32(apiServerPort)
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			host = svc.Status.LoadBalancer.Ingress[0].IP
			port = int32(apiServerPort)
		}
	case hyperv1.NodePort:
		if strategy.NodePort == nil {
			err = fmt.Errorf("strategy details not specified for API server nodeport type service")
			return
		}
		if len(svc.Spec.Ports) == 0 {
			return
		}
		if svc.Spec.Ports[0].NodePort == 0 {
			return
		}
		port = svc.Spec.Ports[0].NodePort
		host = strategy.NodePort.Address
	}
	return
}

func ReconcilePrivateService(svc *corev1.Service, owner *metav1.OwnerReference) error {
	apiServerPort := 6443
	util.EnsureOwnerRef(svc, owner)
	svc.Spec.Selector = kasLabels()
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(apiServerPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(apiServerPort)
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.ObjectMeta.Annotations = map[string]string{
		"service.beta.kubernetes.io/aws-load-balancer-internal": "true",
		"service.beta.kubernetes.io/aws-load-balancer-type":     "nlb",
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcilePrivateServiceStatus(hcpName string) (host string, port int32, err error) {
	return fmt.Sprintf("api.%s.hypershift.local", hcpName), 6443, nil
}
