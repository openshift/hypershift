package kubevirt

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName = "cloud-controller-manager"

	infraKubeconfigVolumeName = "infra-kubeconfig"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	clusterName, ok := hcp.Labels["cluster.x-k8s.io/cluster-name"]
	if !ok {
		return fmt.Errorf("\"cluster.x-k8s.io/cluster-name\" label doesn't exist in HostedControlPlane")
	}

	isExternalInfra := false
	if hcp.Spec.Platform.Kubevirt != nil && hcp.Spec.Platform.Kubevirt.Credentials != nil {
		isExternalInfra = true
	}

	if isExternalInfra {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildInfraKubeconfigVolume())
	}

	util.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			fmt.Sprintf("--cluster-name=%s", clusterName),
		)

		if isExternalInfra {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      infraKubeconfigVolumeName,
				MountPath: "/etc/kubernetes/infra-kubeconfig",
			})
		}
	})
	return nil
}

func buildInfraKubeconfigVolume() corev1.Volume {
	v := corev1.Volume{
		Name: infraKubeconfigVolumeName,
	}
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: hyperv1.KubeVirtInfraCredentialsSecretName,
	}
	return v
}
