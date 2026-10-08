package ingressoperator

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	controlPlaneVersionString := cpContext.ReleaseImageProvider.Version()
	controlPlaneVersion, err := semver.Parse(controlPlaneVersionString)
	if err != nil {
		return fmt.Errorf("failed to parse control plane release version %q: %w", controlPlaneVersionString, err)
	}

	userVersionString := cpContext.UserReleaseImageProvider.Version()
	userVersion, err := semver.Parse(userVersionString)
	if err != nil {
		return fmt.Errorf("failed to parse user release version %q: %w", userVersionString, err)
	}

	// The control plane payload supplies the ingress-operator binary, while the user payload supplies the versioned
	// HAProxy images. Both payloads must support version selection before configuring the optional flags.
	haproxyVersionSelectionSupported := !imageprovider.ImageOverridden(cpContext.ReleaseImageProvider, "cluster-ingress-operator") &&
		supportsHAProxyVersionSelection(controlPlaneVersion) && supportsHAProxyVersionSelection(userVersion)

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "RELEASE_VERSION", Value: userVersionString,
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router"),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "CANARY_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("cluster-ingress-operator"),
		})

		if haproxyVersionSelectionSupported {
			c.Command = append(c.Command,
				"--haproxy-image", "2.8=$(HAPROXY_28_IMAGE)",
				"--haproxy-image", "3.2=$(HAPROXY_32_IMAGE)",
				"--default-haproxy-version", "$(DEFAULT_HAPROXY_VERSION)",
			)
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name: "HAPROXY_28_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router-haproxy28"),
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name: "HAPROXY_32_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router-haproxy32"),
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2",
			})
		}

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

func supportsHAProxyVersionSelection(version semver.Version) bool {
	// HAProxy version selection was introduced in OCP 4.23, which is dual-released as OCP 5.0.
	return version.Major >= 5 || (version.Major == 4 && version.Minor >= 23)
}
