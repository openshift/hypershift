package router

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if swiftPodNetworkInstance := cpContext.HCP.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation]; swiftPodNetworkInstance != "" {
		if deployment.Spec.Template.Labels == nil {
			deployment.Spec.Template.Labels = map[string]string{}
		}
		deployment.Spec.Template.Labels["kubernetes.azure.com/pod-network-instance"] = swiftPodNetworkInstance
	}

	return nil
}
