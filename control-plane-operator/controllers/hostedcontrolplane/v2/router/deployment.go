package router

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
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

	if azureutil.IsAroSwiftEnabled(cpContext.HCP) {
		// In ARO Swift, connections go directly to the router pod without a service,
		// so we need to listen on the standard HTTPS port 443.
		// The NET_BIND_SERVICE capability in the deployment allows binding to privileged ports.
		for i, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == "router" {
				for j, port := range container.Ports {
					if port.Name == "https" {
						deployment.Spec.Template.Spec.Containers[i].Ports[j].ContainerPort = 443
					}
				}
			}
		}
	}

	return nil
}
