package ingress

import (
	_ "embed"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func hcpRouterLabels() map[string]string {
	return map[string]string{
		"app": "private-router",
	}
}

func ReconcileRouterService(svc *corev1.Service, internal, crossZoneLoadBalancingEnabled bool, hcp *hyperv1.HostedControlPlane) error {
	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform {
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
		if internal {
			svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
		}
		if crossZoneLoadBalancingEnabled {
			svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
		}
		util.ApplyAWSLoadBalancerSubnetsAnnotation(svc, hcp)
	}

	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range hcpRouterLabels() {
		svc.Labels[k] = v
	}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Selector = hcpRouterLabels()
	foundHTTPS := false

	for i, port := range svc.Spec.Ports {
		switch port.Name {
		case "https":
			svc.Spec.Ports[i].Port = 443
			svc.Spec.Ports[i].TargetPort = intstr.FromString("https")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundHTTPS = true
		}
	}
	if !foundHTTPS {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "https",
			Port:       443,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	return nil
}

func ReconcileRouteStatus(route *routev1.Route, externalHostname, internalHostname string) {
	var canonicalHostName string
	if _, isInternal := route.Labels[util.InternalRouteLabel]; isInternal {
		canonicalHostName = internalHostname
	} else {
		canonicalHostName = externalHostname
	}

	// Skip reconciliation if ingress status.ingress has already been populated and canonical hostname is the same
	if len(route.Status.Ingress) > 0 && route.Status.Ingress[0].RouterCanonicalHostname == canonicalHostName {
		return
	}

	ingress := routev1.RouteIngress{
		Host:                    route.Spec.Host,
		RouterName:              "router",
		WildcardPolicy:          routev1.WildcardPolicyNone,
		RouterCanonicalHostname: canonicalHostName,
	}

	if len(route.Status.Ingress) > 0 && len(route.Status.Ingress[0].Conditions) > 0 {
		ingress.Conditions = route.Status.Ingress[0].Conditions
	} else {
		now := metav1.Now()
		ingress.Conditions = []routev1.RouteIngressCondition{
			{
				Type:               routev1.RouteAdmitted,
				LastTransitionTime: &now,
				Status:             corev1.ConditionTrue,
			},
		}
	}
	route.Status.Ingress = []routev1.RouteIngress{ingress}
}
