package util

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// InjectHostedClusterKubeconfig injects the hosted cluster kubeconfig into a deployment.
// This allows OLMv1 components running in the management cluster to access the hosted cluster API.
// The admin-kubeconfig secret is mounted as a volume and KUBECONFIG environment variable is set.
func InjectHostedClusterKubeconfig(
	ctx component.WorkloadContext,
	deployment *appsv1.Deployment,
) error {
	kubeconfigPath := "/etc/openshift/kubeconfig/kubeconfig"

	// Add volume for admin-kubeconfig secret
	hostedKubeconfigVolume := corev1.Volume{
		Name: "kubeconfig",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  "admin-kubeconfig",
				DefaultMode: ptr.To[int32](0640),
			},
		},
	}

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, hostedKubeconfigVolume)

	// Mount volume and set KUBECONFIG for the main container
	for i := range deployment.Spec.Template.Spec.Containers {
		container := &deployment.Spec.Template.Spec.Containers[i]

		// Add volume mount
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "kubeconfig",
			MountPath: "/etc/openshift/kubeconfig",
			ReadOnly:  true,
		})

		// Set KUBECONFIG environment variable
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "KUBECONFIG",
			Value: kubeconfigPath,
		})
	}

	return nil
}

// ValidateDeployment performs basic validation on a deployment adapted for hosted cluster access.
func ValidateDeployment(deployment *appsv1.Deployment) error {
	if deployment == nil {
		return fmt.Errorf("deployment is nil")
	}

	// Verify kubeconfig volume exists
	hasKubeconfigVolume := false
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name == "kubeconfig" {
			hasKubeconfigVolume = true
			break
		}
	}
	if !hasKubeconfigVolume {
		return fmt.Errorf("deployment missing kubeconfig volume")
	}

	return nil
}
