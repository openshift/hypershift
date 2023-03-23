package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/util"
)

func ReconcileService(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy, owner *metav1.OwnerReference, apiServerServicePort int, apiServerListenPort int, apiAllowedCIDRBlocks []string, isPublic, isPrivate bool) error {
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
	portSpec.Port = int32(apiServerServicePort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(apiServerListenPort)
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
			if isPrivate {
				// AWS Private link requires endpoint and service endpoints to exist in the same underlying zone.
				// To ensure that requirement is satisfied in Regions with more than 3 zones, managed services create subnets in all of them.
				// That and having this enabled in the load balancers would make the private link communication to always succeed.
				// Without this the connection might go to a subnet without Node and so it would be rejected.
				svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
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
		if message, err := util.CollectLBMessageIfNotProvisioned(svc, messageCollector); err != nil || message != "" {
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
		if message, err := util.CollectLBMessageIfNotProvisioned(svc, messageCollector); err != nil || message != "" {
			return host, port, message, err
		}
		host = strategy.Route.Hostname
		port = int32(apiServerPort)
	}
	return
}

func ReconcilePrivateService(svc *corev1.Service, hcp *hyperv1.HostedControlPlane, owner *metav1.OwnerReference) error {
	apiServerPort := util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort)
	apiServerListenPort := util.BindAPIPortWithDefault(hcp, config.DefaultAPIServerPort)
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
	portSpec.TargetPort = intstr.FromInt(int(apiServerListenPort))
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}

	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcilePrivateServiceStatus(hcp *hyperv1.HostedControlPlane) (host string, port int32, err error) {
	return fmt.Sprintf("api.%s.hypershift.local", hcp.Name), util.InternalAPIPortWithDefault(hcp, config.DefaultAPIServerPort), nil
}

func ReconcileExternalPublicRoute(route *routev1.Route, owner *metav1.OwnerReference, hostname string) error {
	return reconcileExternalRoute(route, owner, hostname)
}

func ReconcileExternalPrivateRoute(route *routev1.Route, owner *metav1.OwnerReference, hostname string) error {
	if err := reconcileExternalRoute(route, owner, hostname); err != nil {
		return err
	}
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[hyperv1.RouteVisibilityLabel] = hyperv1.RouteVisibilityPrivate
	util.AddInternalRouteLabel(route)
	return nil
}

func reconcileExternalRoute(route *routev1.Route, owner *metav1.OwnerReference, hostname string) error {
	if hostname == "" {
		return fmt.Errorf("route hostname is required for service APIServer")
	}
	util.EnsureOwnerRef(route, owner)
	util.AddHCPRouteLabel(route)
	route.Spec.Host = hostname
	route.Spec.To = routev1.RouteTargetReference{
		Kind: "Service",
		Name: manifests.KubeAPIServerService("").Name,
	}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
	// remove annotation as external-dns will register the name if host is within the zone
	delete(route.Annotations, hyperv1.ExternalDNSHostnameAnnotation)
	return nil
}

func ReconcileInternalRoute(route *routev1.Route, owner *metav1.OwnerReference) error {
	util.EnsureOwnerRef(route, owner)
	route.Spec.Host = fmt.Sprintf("api.%s.hypershift.local", owner.Name)
	// Assumes owner is the HCP
	return util.ReconcileInternalRoute(route, "", manifests.KubeAPIServerService("").Name)
}
