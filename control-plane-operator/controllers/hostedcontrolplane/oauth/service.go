package oauth

import (
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

func ReconcileService(svc *corev1.Service, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy, platformType hyperv1.PlatformType) error {
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
	}
	return
}
