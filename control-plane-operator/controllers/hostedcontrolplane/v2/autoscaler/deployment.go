package autoscaler

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const (
	kubeconfigVolumeName = "kubeconfig"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// TODO if the options for the cluster autoscaler continues to grow, we should take inspiration
		// from the cluster-autoscaler-operator and create some utility functions for these assignments.
		options := hcp.Spec.Autoscaling
		if options.MaxNodesTotal != nil {
			c.Args = append(c.Args, fmt.Sprintf("--max-nodes-total=%d", *options.MaxNodesTotal))
		}

		if options.MaxPodGracePeriod != nil {
			c.Args = append(c.Args, fmt.Sprintf("--max-graceful-termination-sec=%d", *options.MaxPodGracePeriod))
		}

		if options.MaxNodeProvisionTime != "" {
			c.Args = append(c.Args, fmt.Sprintf("--max-node-provision-time=%s", options.MaxNodeProvisionTime))
		}

		if options.PodPriorityThreshold != nil {
			c.Args = append(c.Args, fmt.Sprintf("--expendable-pods-priority-cutoff=%d", *options.PodPriorityThreshold))
		}
	})

	util.UpdateVolume(kubeconfigVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID).Name
	})

	deployment.Spec.Replicas = ptr.To[int32](1)
	if _, exists := hcp.Annotations[hyperv1.DisableClusterAutoscalerAnnotation]; exists {
		deployment.Spec.Replicas = ptr.To[int32](0)
	}

	return nil
}
