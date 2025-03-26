package controlplanecomponent

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ServiceAccountKubeconfigVolumeName = "service-account-kubeconfig"
)

func (c *controlPlaneWorkload[T]) adaptServiceAccountKubeconfigSecret(cpContext WorkloadContext, secret *corev1.Secret) error {
	csrSigner := manifests.CSRSignerCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get cluster-signer-ca secret: %v", err)
	}
	rootCA := manifests.RootCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}
	rootCACM := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(rootCA.Data[certs.CASignerCertMapKey]),
		},
	}

	return pki.ReconcileServiceAccountKubeconfig(secret, csrSigner, rootCACM, cpContext.HCP, c.serviceAccountKubeConfigOpts.Namespace, c.serviceAccountKubeConfigOpts.Name)
}

func (c *controlPlaneWorkload[T]) serviceAccountKubeconfigSecretName() string {
	return c.name + "-service-account-kubeconfig"
}

func (c *controlPlaneWorkload[T]) addServiceAccountKubeconfigVolumes(podTemplateSpec *corev1.PodTemplateSpec) {
	volumeName := "service-account-kubeconfig"
	volume := corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				DefaultMode: ptr.To[int32](416),
				SecretName:  c.serviceAccountKubeconfigSecretName(),
			},
		},
	}
	podTemplateSpec.Spec.Volumes = append(podTemplateSpec.Spec.Volumes, volume)

	containerName := c.serviceAccountKubeConfigOpts.ContainerName
	for i := range podTemplateSpec.Spec.Containers {
		// if containerName is specified, only mount to this container, otherwise mount to all containers.
		if containerName != "" && containerName != podTemplateSpec.Spec.Containers[i].Name {
			continue
		}

		volumeMount := corev1.VolumeMount{
			Name:      volumeName,
			MountPath: c.serviceAccountKubeConfigOpts.MountPath,
		}
		podTemplateSpec.Spec.Containers[i].VolumeMounts = append(podTemplateSpec.Spec.Containers[i].VolumeMounts, volumeMount)
	}
}

func (c *controlPlaneWorkload[T]) serviceAccountKubeconfigSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.serviceAccountKubeconfigSecretName(),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": []byte(""),
			"ca.crt":     []byte(""),
			"tls.crt":    []byte(""),
			"tls.key":    []byte(""),
		},
		Type: corev1.SecretTypeOpaque,
	}
}
