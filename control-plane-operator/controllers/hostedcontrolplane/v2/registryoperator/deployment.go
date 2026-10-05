package registryoperator

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	versionStr := cpContext.ReleaseImageProvider.Version()
	version, err := semver.Parse(versionStr)
	if err != nil {
		return fmt.Errorf("failed to parse control plane release version (%s): %w", versionStr, err)
	}
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// cluster-image-registry-operator 89b3f48903c7 added --config in 4.22.
		// Retain the older metrics server's defaults and certificate file watches.
		if version.Major == 4 && version.Minor < 22 {
			for i := range c.Args {
				c.Args[i] = strings.ReplaceAll(c.Args[i], "--config=/var/run/configmaps/image-registry-controller-config/config.yaml", "")
			}
		}
		proxy.SetEnvVars(&c.Env)

		version := cpContext.UserReleaseImageProvider.Version()
		podspec.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: version},
			{Name: "OPERATOR_IMAGE_VERSION", Value: version},
			{Name: "OPERATOR_IMAGE", Value: cpContext.ReleaseImageProvider.GetImage("cluster-image-registry-operator")},
			{Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("docker-registry")},
			{Name: "IMAGE_PRUNER", Value: cpContext.UserReleaseImageProvider.GetImage("cli")},
		})

		// For managed Azure deployments, we pass an environment variable, MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH, so
		// we authenticate with Azure API through UserAssignedCredential authentication. We also mount the
		// SecretProviderClass for the Secrets Store CSI driver to use; it will grab the JSON object stored in the
		// MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH and mount it as a volume in the image registry pod in the path.
		if azureutil.IsAroHCPByHCP(cpContext.HCP) {
			c.Env = append(c.Env,
				azureutil.CreateEnvVarsForAzureManagedIdentity(cpContext.HCP.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ImageRegistry.CredentialsSecretName)...)

			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName),
			)
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
				azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName, config.ManagedAzureImageRegistrySecretStoreProviderClassName),
			)
		}
	})

	return nil
}
