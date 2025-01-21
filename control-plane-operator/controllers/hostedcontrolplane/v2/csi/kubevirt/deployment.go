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
	containerName = "csi-driver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if !isExternalInfraKubvirt(cpContext.HCP) {
		return nil
	}

	const infraClusterKubeconfigMount = "/var/run/secrets/infracluster"

	util.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--infra-cluster-kubeconfig=%s/kubeconfig", infraClusterKubeconfigMount))

		externalKubeconfigVolumeMount := corev1.VolumeMount{
			Name:      "infracluster",
			MountPath: infraClusterKubeconfigMount,
		}
		c.VolumeMounts = append(c.VolumeMounts, externalKubeconfigVolumeMount)
	})

	infraClusterVolume := corev1.Volume{
		Name: "infracluster",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: hyperv1.KubeVirtInfraCredentialsSecretName,
			},
		},
	}
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, infraClusterVolume)

	return nil
}

func isExternalInfraKubvirt(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraKubeConfigSecret != nil &&
		hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace != "" {
		return true
	} else {
		return false
	}
}
