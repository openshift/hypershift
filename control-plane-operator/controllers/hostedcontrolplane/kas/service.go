package kas

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/util/intstr"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileService(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy, owner *metav1.OwnerReference, apiServerPort int, apiAllowedCIDRBlocks []string, isPublic bool) error {
	util.EnsureOwnerRef(svc, owner)
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = kasLabels()
	}
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
	portSpec.TargetPort = intstr.FromInt(APIServerListenPort)
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if isPublic {
			svc.Spec.Type = corev1.ServiceTypeLoadBalancer
			if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
				svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = strategy.LoadBalancer.Hostname
			}
		} else {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	case hyperv1.Route:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	default:
		return fmt.Errorf("invalid publishing strategy for Kube API server service: %s", strategy.Type)
	}
	svc.Spec.LoadBalancerSourceRanges = apiAllowedCIDRBlocks
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileServiceStatus(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy, apiServerPort int, messageCollector events.MessageCollector) (host string, port int32, message string, err error) {
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if message, err := collectLBMessageIfNotProvisioned(svc, messageCollector); err != nil || message != "" {
			return host, port, message, err
		}
		port = int32(apiServerPort)
		switch {
		case strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "":
			host = strategy.LoadBalancer.Hostname
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			host = svc.Status.LoadBalancer.Ingress[0].IP
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
	case hyperv1.Route:
		if message, err := collectLBMessageIfNotProvisioned(svc, messageCollector); err != nil || message != "" {
			return host, port, message, err
		}
		host = strategy.Route.Hostname
		port = int32(apiServerPort)
	}
	return
}

func ReconcilePrivateService(svc *corev1.Service, hcp *hyperv1.HostedControlPlane, owner *metav1.OwnerReference) error {
	apiServerPort := util.APIPortWithDefault(hcp, config.DefaultAPIServerPort)
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
	portSpec.TargetPort = intstr.FromInt(APIServerListenPort)
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
	svc.Spec.Ports[0] = portSpec
	return nil
}

func collectLBMessageIfNotProvisioned(svc *corev1.Service, messageCollector events.MessageCollector) (string, error) {
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		return "", nil

	}
	message := fmt.Sprintf("Kubernetes APIServer load balancer is not provisioned; %v since creation.", duration.ShortHumanDuration(time.Since(svc.ObjectMeta.CreationTimestamp.Time)))
	var eventMessages []string
	eventMessages, err := messageCollector.ErrorMessages(svc)
	if err != nil {
		return message, fmt.Errorf("failed to get events for service %s/%s: %w", svc.Namespace, svc.Name, err)
	}
	if len(eventMessages) > 0 {
		message = fmt.Sprintf("Kubernetes APIServer load balancer is not provisioned: %s", strings.Join(eventMessages, "; "))
	}

	return message, nil
}

func ReconcilePrivateServiceStatus(hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	return fmt.Sprintf("api.%s.hypershift.local", hcp.Name), util.APIPortWithDefault(hcp, config.DefaultAPIServerPort), nil
}

func ReconcileRoute(route *routev1.Route, hostname string) {
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[ingress.HypershiftRouteLabel] = route.Namespace
	if route.CreationTimestamp.IsZero() {
		route.Spec.Host = hostname
	}
	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: manifests.KubeAPIServerService("").Name,
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
}
