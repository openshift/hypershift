package podspec

import (
	"context"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

// HasTerminatingPods returns true if any pods matching the given label selector
// have a non-nil DeletionTimestamp (i.e. are still shutting down).
// Returns false with a nil error when the selector is nil.
func HasTerminatingPods(ctx context.Context, c client.Reader, namespace string, selector *metav1.LabelSelector) (bool, error) {
	if selector == nil {
		return false, nil
	}
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return false, fmt.Errorf("failed to parse label selector: %w", err)
	}
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: labelSelector}); err != nil {
		return false, fmt.Errorf("failed to list pods for termination check: %w", err)
	}
	for i := range podList.Items {
		if podList.Items[i].DeletionTimestamp != nil {
			return true, nil
		}
	}
	return false, nil
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
				Optional:             ptr.To(true),
			},
		},
	})
}
