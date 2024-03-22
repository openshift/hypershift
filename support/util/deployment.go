package util

import (
	"context"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
)

func IsDeploymentReady(ctx context.Context, deployment *appsv1.Deployment) bool {
	if *deployment.Spec.Replicas != deployment.Status.AvailableReplicas ||
		*deployment.Spec.Replicas != deployment.Status.ReadyReplicas ||
		*deployment.Spec.Replicas != deployment.Status.UpdatedReplicas ||
		*deployment.Spec.Replicas != deployment.Status.Replicas ||
		deployment.Status.UnavailableReplicas != 0 ||
		deployment.ObjectMeta.Generation != deployment.Status.ObservedGeneration {
		return false
	}

	return true
}

func IsStatefulSetReady(ctx context.Context, statefulSet *appsv1.StatefulSet) bool {
	if *statefulSet.Spec.Replicas != statefulSet.Status.AvailableReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.ReadyReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.UpdatedReplicas ||
		*statefulSet.Spec.Replicas != statefulSet.Status.Replicas ||
		statefulSet.ObjectMeta.Generation != statefulSet.Status.ObservedGeneration {
		return false
	}

	return true
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

func DeploymentAddOpenShiftTrustedCABundleConfigMap(deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "openshift-config-managed-trusted-ca-bundle",
		MountPath: "/etc/pki/ca-trust/extracted/pem",
		ReadOnly:  true,
	})
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "openshift-config-managed-trusted-ca-bundle",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "openshift-config-managed-trusted-ca-bundle"},
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "tls-ca-bundle.pem"}},
				Optional:             pointer.Bool(true),
			},
		},
	})
}
