package ingress

import (
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	HypershiftRouteLabel = "hypershift.openshift.io/hosted-control-plane"
)

func ReconcilePrivateIngressController(ingressController *operatorv1.IngressController, name, domain string, platformType hyperv1.PlatformType) error {
	ingressController.Spec.Domain = domain
	ingressController.Spec.RouteSelector = &v1.LabelSelector{
		MatchLabels: map[string]string{
			HypershiftRouteLabel: name,
		},
	}
	switch platformType {
	case hyperv1.AWSPlatform:
		ingressController.Spec.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
			LoadBalancer: &operatorv1.LoadBalancerStrategy{
				Scope: operatorv1.InternalLoadBalancer,
				ProviderParameters: &operatorv1.ProviderLoadBalancerParameters{
					Type: operatorv1.AWSLoadBalancerProvider,
					AWS: &operatorv1.AWSLoadBalancerParameters{
						Type: operatorv1.AWSNetworkLoadBalancer,
					},
				},
			},
		}
	default:
		return fmt.Errorf("private clusters are not supported on platform %s", platformType)
	}

	return nil
}
