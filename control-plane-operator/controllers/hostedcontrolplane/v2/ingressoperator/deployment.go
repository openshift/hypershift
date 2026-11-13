package ingressoperator

import (
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/omnicpo"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version(),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router"),
		})
		// Render for main: the multi-HAProxy scheme (HAPROXY_32_IMAGE env + the --haproxy-image /
		// --default-haproxy-version / --deprecated-haproxy-version command flags). OMNI-CPO strips
		// these back to the single-image shape on pre-4.23 operands (see omniCPOTransforms).
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "HAPROXY_32_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("haproxy-router-haproxy32"),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2",
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name: "DEPRECATED_HAPROXY_VERSION", Value: "",
		})
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

// omniCPOTransforms lists the drift adjustments a main-built CPO applies to the ingress-operator
// Deployment when it drives an older control plane. See support/omnicpo.
func omniCPOTransforms() []omnicpo.Transform[*appsv1.Deployment] {
	return []omnicpo.Transform[*appsv1.Deployment]{
		{
			// The multi-HAProxy scheme (HAPROXY_32_IMAGE env + the --haproxy-image /
			// --default-haproxy-version / --deprecated-haproxy-version command flags plus the
			// haproxy-router-haproxy32 image) arrived at 4.23. A pre-4.23 ingress-operator rejects
			// those flags and its payload lacks the haproxy32 image (which would flip
			// ValidReleaseInfo), so fall back to the single-image shape.
			Reason: "pre-4.23 ingress-operator rejects the multi-HAProxy env vars",
			When:   omnicpo.Below(config.Version423),
			Do:     omnicpo.StripEnv(ComponentName, "HAPROXY_32_IMAGE", "DEFAULT_HAPROXY_VERSION", "DEPRECATED_HAPROXY_VERSION"),
		},
		{
			Reason: "pre-4.23 ingress-operator rejects the multi-HAProxy command flags",
			When:   omnicpo.Below(config.Version423),
			Do:     omnicpo.StripCommandFlags(ComponentName, "--haproxy-image", "--default-haproxy-version", "--deprecated-haproxy-version"),
		},
	}
}
