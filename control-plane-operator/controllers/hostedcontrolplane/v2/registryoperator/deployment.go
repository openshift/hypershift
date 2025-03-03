package registryoperator

import (
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		proxy.SetEnvVars(&c.Env)

		version := cpContext.UserReleaseImageProvider.Version()
		util.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: version},
			{Name: "OPERATOR_IMAGE_VERSION", Value: version},
			{Name: "OPERATOR_IMAGE", Value: cpContext.ReleaseImageProvider.GetImage("cluster-image-registry-operator")},
			{Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("docker-registry")},
			{Name: "IMAGE_PRUNER", Value: cpContext.UserReleaseImageProvider.GetImage("cli")},
		})

		// For managed Azure deployments, we pass environment variables so we authenticate with Azure API through certificate
		// authentication. We also mount the SecretProviderClass for the Secrets Store CSI driver to use; it will grab the
		// certificate related to the ARO_HCP_MI_CLIENT_ID and mount it as a volume in the ingress pod in the path,
		// ARO_HCP_CLIENT_CERTIFICATE_PATH.
		if azureutil.IsAroHCP() {
			managedIdentiity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ImageRegistry

			c.Env = append(c.Env,
				azureutil.CreateEnvVarsForAzureManagedIdentity(managedIdentiity.ClientID, cpContext.HCP.Spec.Platform.Azure.TenantID, managedIdentiity.CertificateName, managedIdentiity.CredentialsSecretName)...)

			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName),
			)
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
				azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureImageRegistrySecretStoreVolumeName, config.ManagedAzureImageRegistrySecretStoreProviderClassName),
			)
		}
	})

	util.UpdateContainer("client-token-minter", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, "--token-audience", cpContext.HCP.Spec.IssuerURL)
	})

	if cpContext.HCP.Spec.Platform.Type != v1beta1.AWSPlatform {
		util.RemoveContainer("token-minter", &deployment.Spec.Template.Spec)
	}
	return nil
}
