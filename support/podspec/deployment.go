package podspec

import (
	"context"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func IsDeploymentReady(_ context.Context, deployment *appsv1.Deployment) bool {
	if ptr.Deref(deployment.Spec.Replicas, 0) != deployment.Status.AvailableReplicas ||
		ptr.Deref(deployment.Spec.Replicas, 0) != deployment.Status.ReadyReplicas ||
		ptr.Deref(deployment.Spec.Replicas, 0) != deployment.Status.UpdatedReplicas ||
		ptr.Deref(deployment.Spec.Replicas, 0) != deployment.Status.Replicas ||
		deployment.Status.UnavailableReplicas != 0 ||
		deployment.ObjectMeta.Generation != deployment.Status.ObservedGeneration {
		return false
	}

	return true
}

func IsStatefulSetReady(_ context.Context, statefulSet *appsv1.StatefulSet) bool {
	if ptr.Deref(statefulSet.Spec.Replicas, 0) != statefulSet.Status.AvailableReplicas ||
		ptr.Deref(statefulSet.Spec.Replicas, 0) != statefulSet.Status.ReadyReplicas ||
		ptr.Deref(statefulSet.Spec.Replicas, 0) != statefulSet.Status.UpdatedReplicas ||
		ptr.Deref(statefulSet.Spec.Replicas, 0) != statefulSet.Status.Replicas ||
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
