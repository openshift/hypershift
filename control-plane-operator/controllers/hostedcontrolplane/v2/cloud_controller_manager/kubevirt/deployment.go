package kubevirt

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

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

	podspec.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			fmt.Sprintf("--cluster-name=%s", clusterName),
		)

		// Add TLS configuration based on cluster TLS security profile
		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

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
