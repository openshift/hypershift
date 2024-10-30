package openstack

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	containerName = "cloud-controller-manager"

	secretOCCMVolumeName = "secret-occm"
	trustedCAVolumeName  = "trusted-ca"

	CaDir       = "/etc/pki/ca-trust/extracted/pem"
	CABundleKey = "ca-bundle.pem"
	CASecretKey = "cacert"
)

func adaptDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.Platform.OpenStack == nil {
		return fmt.Errorf(".spec.platform.openStack is not defined")
	}

	credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.OpenStack.IdentityRef.Name}}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return fmt.Errorf("failed to get OpenStack credentials secret: %w", err)
	}
	hasCACert := GetCACertFromCredentialsSecret(credentialsSecret) != nil

	if hasCACert {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildTrustedCAVolume())
	}

	util.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "OCP_INFRASTRUCTURE_NAME",
			Value: cpContext.HCP.Spec.InfraID,
		})

		if hasCACert {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      trustedCAVolumeName,
				MountPath: CaDir,
				ReadOnly:  true,
			})
		}
	})

	util.UpdateVolume(secretOCCMVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = hcp.Spec.Platform.OpenStack.IdentityRef.Name
	})
	return nil
}

func buildTrustedCAVolume() corev1.Volume {
	v := corev1.Volume{
		Name: trustedCAVolumeName,
	}
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: manifests.OpenStackTrustedCA("").Name},
		Items: []corev1.KeyToPath{
			{
				Key:  CABundleKey,
				Path: CABundleKey,
			},
		},
	}
	return v
}

// GetCloudConfigFromCredentialsSecret returns the CA cert from the credentials secret.
func GetCACertFromCredentialsSecret(secret *corev1.Secret) []byte {
	caCert, ok := secret.Data[CASecretKey]
	if !ok {
		return nil
	}
	return caCert
}
