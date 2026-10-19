package ingressoperator

import (
	"fmt"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

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
	userVersionStr := cpContext.UserReleaseImageProvider.Version()
	userVersion, err := semver.Parse(userVersionStr)
	if err != nil {
		return fmt.Errorf("failed to parse user release version (%s): %w", userVersionStr, err)
	}
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version(),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router"),
		})
		// Selection flags require a 4.23+ operator, including prereleases.
		if version.Major >= 5 || (version.Major == 4 && version.Minor >= 23) {
			haproxy28, _ := cpContext.UserReleaseImageProvider.ImageExist("haproxy-router-haproxy28")
			haproxy32, _ := cpContext.UserReleaseImageProvider.ImageExist("haproxy-router-haproxy32")
			requireSelection := userVersion.Major >= 5 || (userVersion.Major == 4 && userVersion.Minor >= 23)
			if requireSelection {
				// New payloads must provide both images; GetImage records absent or empty values.
				haproxy28 = cpContext.UserReleaseImageProvider.GetImage("haproxy-router-haproxy28")
				haproxy32 = cpContext.UserReleaseImageProvider.GetImage("haproxy-router-haproxy32")
			}
			// Older user payloads can still use --image alone without selection images.
			if requireSelection || (haproxy28 != "" && haproxy32 != "") {
				c.Command = append(c.Command,
					"--haproxy-image", "2.8=$(HAPROXY_28_IMAGE)",
					"--haproxy-image", "3.2=$(HAPROXY_32_IMAGE)",
					"--default-haproxy-version", "$(DEFAULT_HAPROXY_VERSION)",
				)
				podspec.UpsertEnvVars(c, []corev1.EnvVar{
					{Name: "HAPROXY_28_IMAGE", Value: haproxy28},
					{Name: "HAPROXY_32_IMAGE", Value: haproxy32},
					{Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2"},
				})
			}
		}
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "CANARY_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("cluster-ingress-operator"),
		})

		if cpContext.HCP.Spec.FIPS {
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name: "FIPS_ENABLED", Value: "true",
			})
		}

		// For managed Azure deployments, we pass an environment variable, MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH, so
		// we authenticate with Azure API through UserAssignedCredential authentication. We also mount the
		// SecretProviderClass for the Secrets Store CSI driver to use; it will grab the JSON object stored in the
		// MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH and mount it as a volume in the ingress pod in the path.
		if azureutil.IsAroHCPByHCP(cpContext.HCP) {
			c.Env = append(c.Env,
				azureutil.CreateEnvVarsForAzureManagedIdentity(cpContext.HCP.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Ingress.CredentialsSecretName)...)

			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureIngressSecretStoreVolumeName),
			)
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
				azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureIngressSecretStoreVolumeName, config.ManagedAzureIngressSecretStoreProviderClassName),
			)
		}
	})

	return nil
}
