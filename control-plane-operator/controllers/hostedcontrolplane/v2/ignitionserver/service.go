package ignitionserver

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptService(cpContext component.WorkloadContext, svc *corev1.Service) error {
	if cpContext.HCP.Spec.Platform.Type != hyperv1.IBMCloudPlatform {
		return nil
	}

	existingServiceUsesNodePort := false
	existingService := &corev1.Service{}
	if err := cpContext.Client.Get(cpContext.Context, client.ObjectKeyFromObject(svc), existingService); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get existing ignition service: %w", err)
		}
	} else {
		if len(existingService.Spec.Ports) != 1 {
			return fmt.Errorf("existing ignition service must have exactly one port exposed, this has: %d", len(existingService.Spec.Ports))
		}
		existingServiceUsesNodePort = existingService.Spec.Type == corev1.ServiceTypeNodePort
	}

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
		if existingServiceUsesNodePort {
			svc.Spec.Type = corev1.ServiceTypeNodePort
			if strategy.NodePort != nil {
				svc.Spec.Ports[0].NodePort = strategy.NodePort.Port
			} else {
				svc.Spec.Ports[0].NodePort = existingService.Spec.Ports[0].NodePort
			}
		}
	default:
		return fmt.Errorf("invalid publishing strategy for Ignition service: %s", strategy.Type)
	}

	return nil
}
