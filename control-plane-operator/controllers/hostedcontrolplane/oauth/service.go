package oauth

import (
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	azureutil "github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	OAuthServerPort   = 6443
	RouteExternalPort = 443
)

var (
	oauthServerLabels = map[string]string{
		"app":                              "oauth-openshift",
		hyperv1.ControlPlaneComponentLabel: "oauth-openshift",
	}
)

func ReconcileService(svc *corev1.Service, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy, platformType hyperv1.PlatformType, isPrivate bool) error {
	ownerRef.ApplyTo(svc)
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = oauthServerLabels
	}
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}

	// Setting this to PreferDualStack will make the service to be created with IPv4 and IPv6 addresses if the management cluster is dual stack.
	IPFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack
	svc.Spec.IPFamilyPolicy = &IPFamilyPolicy

	portSpec.Port = int32(OAuthServerPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(OAuthServerPort)
	switch strategy.Type {
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	case hyperv1.Route:
		if ((platformType == hyperv1.IBMCloudPlatform) && (svc.Spec.Type != corev1.ServiceTypeNodePort)) || (platformType != hyperv1.IBMCloudPlatform) {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
	case hyperv1.LoadBalancer:
		if platformType != hyperv1.AzurePlatform {
			return fmt.Errorf("LoadBalancer publishing strategy for OAuth service is only supported on self-managed Azure, got platform: %s", platformType)
		}
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		// Azure uses port 443 for the OAuth LB service because port 6443 collides with
		// the management cluster's KAS on the shared Azure internal load balancer.
		// The target port remains 6443 as that is what the OAuth server pod listens on.
		portSpec.Port = int32(RouteExternalPort)
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = strategy.LoadBalancer.Hostname
		}
		if isPrivate {
			svc.Annotations[azureutil.InternalLoadBalancerAnnotation] = azureutil.InternalLoadBalancerValue
		} else {
			delete(svc.Annotations, azureutil.InternalLoadBalancerAnnotation)
		}
	default:
		return fmt.Errorf("invalid publishing strategy for OAuth service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileServiceStatus(svc *corev1.Service, route *routev1.Route, strategy *hyperv1.ServicePublishingStrategy) (host string, port int32, message string, err error) {
	switch strategy.Type {
	case hyperv1.Route:
		if strategy.Route != nil && strategy.Route.Hostname != "" {
			host = strategy.Route.Hostname
			port = RouteExternalPort
			return
		}
		if route.Spec.Host == "" {
			message = fmt.Sprintf("OAuth service route does not contain valid host; %v since creation", duration.ShortHumanDuration(time.Since(route.ObjectMeta.CreationTimestamp.Time)))
			return
		}
		port = RouteExternalPort
		host = route.Spec.Host
	case hyperv1.NodePort:
		if strategy.NodePort == nil {
			err = fmt.Errorf("strategy details not specified for OAuth nodeport type service")
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
	case hyperv1.LoadBalancer:
		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			host = strategy.LoadBalancer.Hostname
			port = RouteExternalPort
			return
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			message = fmt.Sprintf("OAuth LoadBalancer not yet provisioned; %v since creation", duration.ShortHumanDuration(time.Since(svc.ObjectMeta.CreationTimestamp.Time)))
			return
		}
		ingress := svc.Status.LoadBalancer.Ingress[0]
		if ingress.Hostname != "" {
			host = ingress.Hostname
		} else if ingress.IP != "" {
			host = ingress.IP
		} else {
			message = fmt.Sprintf("OAuth LoadBalancer ingress has no hostname or IP; %v since creation", duration.ShortHumanDuration(time.Since(svc.ObjectMeta.CreationTimestamp.Time)))
			return
		}
		port = RouteExternalPort
	}
	return
}
