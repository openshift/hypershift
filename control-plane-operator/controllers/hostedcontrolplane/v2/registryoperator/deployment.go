package registryoperator

import (
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		proxy.SetEnvVars(&c.Env)

		version := cpContext.UserReleaseImageProvider.Version()
		podspec.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: version},
			{Name: "OPERATOR_IMAGE_VERSION", Value: version},
			// OPERATOR_IMAGE does not set this operator's own image, which comes from
			// the control plane release via the deployment asset. The operator only
			// reads it to stamp an image into resources it creates in the guest
			// cluster: the node-ca DaemonSet and the Azure path fix Job, both in the
			// guest's openshift-image-registry namespace. Those are data plane
			// workloads, so this must come from the user release.
			{Name: "OPERATOR_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("cluster-image-registry-operator")},
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
