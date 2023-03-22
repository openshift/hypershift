package util

import (
	"context"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func IsDeploymentReady(ctx context.Context, c crclient.Client, deployment *appsv1.Deployment) (bool, error) {
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
		return false, fmt.Errorf("failed to fetch %s deployment: %w", deployment.Name, err)
	}

	if *deployment.Spec.Replicas != deployment.Status.AvailableReplicas ||
		*deployment.Spec.Replicas != deployment.Status.ReadyReplicas ||
		*deployment.Spec.Replicas != deployment.Status.UpdatedReplicas ||
		deployment.ObjectMeta.Generation > deployment.Status.ObservedGeneration {
		return false, nil
	}

	return true, nil
}

func DeploymentAddKubevirtInfraCredentials(deployment *appsv1.Deployment) {
	volumeName := "kubevirt-infra-kubeconfig"
	volumeMountPath := "/etc/kubernetes/kubevirt-infra-kubeconfig"
	kubeconfigKey := "kubeconfig"

	deployment.Spec.Template.Spec.Containers[0].VolumeMounts =
		append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: volumeMountPath,
			ReadOnly:  true,
		})

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: hyperv1.KubeVirtInfraCredentialsSecretName,
			},
		},
	})

	deployment.Spec.Template.Spec.Containers[0].Command =
		append(deployment.Spec.Template.Spec.Containers[0].Command, fmt.Sprintf("--kubevirt-infra-kubeconfig=%s", path.Join(volumeMountPath, kubeconfigKey)))
}
