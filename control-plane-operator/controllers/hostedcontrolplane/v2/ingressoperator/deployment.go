package ingressoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version(),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router"),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "CANARY_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("cluster-ingress-operator"),
		})

		if hcp.Spec.FIPS {
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name: "FIPS_ENABLED", Value: "true",
			})
		}

		// For managed Azure deployments, we pass an environment variable, MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH, so
		// we authenticate with Azure API through UserAssignedCredential authentication. We also mount the
		// SecretProviderClass for the Secrets Store CSI driver to use; it will grab the JSON object stored in the
		// MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH and mount it as a volume in the ingress pod in the path.
		if azureutil.IsAroHCPByHCP(hcp) {
			c.Env = append(c.Env,
				azureutil.CreateEnvVarsForAzureManagedIdentity(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Ingress.CredentialsSecretName)...)

			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureIngressSecretStoreVolumeName),
			)
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
				azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureIngressSecretStoreVolumeName, config.ManagedAzureIngressSecretStoreProviderClassName),
			)
		}
	})

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform && hcp.Spec.AdditionalTrustBundle != nil {
		podspec.DeploymentAddAWSCABundleVolume(hcp.Spec.AdditionalTrustBundle, deployment, cpContext.ReleaseImageProvider.GetImage(podspec.CPOImageName))
	}

	return nil
}
