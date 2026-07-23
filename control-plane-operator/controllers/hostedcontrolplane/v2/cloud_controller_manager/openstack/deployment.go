package openstack

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName = "cloud-controller-manager"

	secretOCCMVolumeName = "secret-occm"
	trustedCAVolumeName  = "trusted-ca"

	CADir       = "/etc/pki/ca-trust/extracted/pem"
	CASecretKey = "cacert"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	credentialsSecret, err := getCredentialsSecret(cpContext)
	if err != nil {
		return err
	}

	hasCACert := GetCACertFromCredentialsSecret(credentialsSecret) != nil
	if hasCACert {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildTrustedCAVolume())
	}

	podspec.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "OCP_INFRASTRUCTURE_NAME",
			Value: cpContext.HCP.Spec.InfraID,
		})

		// Add TLS configuration based on cluster TLS security profile
		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

		if hasCACert {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      trustedCAVolumeName,
				MountPath: CADir,
				ReadOnly:  true,
			})
		}
	})

	podspec.UpdateVolume(secretOCCMVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = credentialsSecret.Name
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
