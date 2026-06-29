package azure

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName = "cloud-controller-manager"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	podspec.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			fmt.Sprintf("--cluster-name=%s", cpContext.HCP.Spec.InfraID),
		)

		// Add TLS configuration based on cluster TLS security profile
		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

		if azureutil.IsAroHCPByHCP(cpContext.HCP) {
			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName),
			)
		}
	})

	if azureutil.IsAroHCPByHCP(cpContext.HCP) {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName, config.ManagedAzureCloudProviderSecretProviderClassName),
		)
	} else {
		deployment.Spec.Template.Spec.ServiceAccountName = "azure-cloud-controller-manager"
	}

	return nil
}
