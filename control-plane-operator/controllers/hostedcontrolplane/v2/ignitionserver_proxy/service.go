package ignitionserverproxy

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

func adaptService(cpContext component.WorkloadContext, svc *corev1.Service) error {
	strategy := util.ServicePublishingStrategyByTypeForHCP(cpContext.HCP, hyperv1.Ignition)
	if strategy == nil {
		return fmt.Errorf("ignition service strategy not specified")
	}

	switch strategy.Type {
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if strategy.NodePort != nil {
			svc.Spec.Ports[0].NodePort = strategy.NodePort.Port
		}
	case hyperv1.Route:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	default:
		return fmt.Errorf("invalid publishing strategy for Ignition service: %s", strategy.Type)
	}

	return nil
}
