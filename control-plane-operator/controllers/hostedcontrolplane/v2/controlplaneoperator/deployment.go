package controlplaneoperator

import (
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	managedServiceEnvVar = "MANAGED_SERVICE"
)

func (cpo *ControlPlaneOperatorOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = cpo.Image

		c.Args = append(c.Args,
			fmt.Sprintf("--enable-ci-debug-output=%t", cpContext.EnableCIDebugOutput),
			fmt.Sprintf("--registry-overrides=%s", cpo.RegistryOverrideCommandLine),
		)

		if !cpo.HasUtilities {
			c.Args = append(c.Args,
				"--socks5-proxy-image", cpo.UtilitiesImage,
				"--availability-prober-image", cpo.UtilitiesImage,
				"--token-minter-image", cpo.UtilitiesImage,
			)
		}

		if imageOverrides := cpo.HostedCluster.Annotations[hyperv1.ImageOverridesAnnotation]; imageOverrides != "" {
			c.Args = append(c.Args,
				"--image-overrides", imageOverrides,
			)
		}

		c.Env = append(c.Env, []corev1.EnvVar{
			{
				Name:  "CONTROL_PLANE_OPERATOR_IMAGE",
				Value: cpo.Image,
			},
			{
				Name:  "HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE",
				Value: cpo.Image,
			},
			{
				Name:  "SOCKS5_PROXY_IMAGE",
				Value: cpo.UtilitiesImage,
			},
			{
				Name:  "AVAILABILITY_PROBER_IMAGE",
				Value: cpo.UtilitiesImage,
			},
			{
				Name:  "TOKEN_MINTER_IMAGE",
				Value: cpo.UtilitiesImage,
			},
			{
				Name:  "OPERATE_ON_RELEASE_IMAGE",
				Value: util.HCPControlPlaneReleaseImage(hcp),
			},
			{
				Name:  "OPENSHIFT_IMG_OVERRIDES",
				Value: cpo.OpenShiftRegistryOverrides,
			},
			{
				Name:  "CERT_ROTATION_SCALE",
				Value: cpo.CertRotationScale.String(),
			},
			{
				Name:  "HYPERSHIFT_FEATURESET",
				Value: string(cpo.FeatureSet),
			},
			metrics.MetricsSetToEnv(cpContext.MetricsSet),
		}...)

		proxy.SetEnvVars(&c.Env)

		if certValidity := cpo.HostedCluster.Annotations[certs.CertificateValidityAnnotation]; certValidity != "" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  certs.CertificateValidityEnvVar,
					Value: certValidity,
				},
			)
		}
		if certRenewalPercentage := cpo.HostedCluster.Annotations[certs.CertificateRenewalAnnotation]; certRenewalPercentage != "" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  certs.CertificateRenewalEnvVar,
					Value: certRenewalPercentage,
				},
			)
		}
		if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  rhobsmonitoring.EnvironmentVariable,
					Value: "1",
				},
			)
		}
		if os.Getenv("ENABLE_SIZE_TAGGING") == "1" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "ENABLE_SIZE_TAGGING",
					Value: "1",
				},
			)
		}

		if envImage := os.Getenv(images.KonnectivityEnvVar); len(envImage) > 0 {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  images.KonnectivityEnvVar,
					Value: envImage,
				},
			)
		}

		if os.Getenv(config.EnableCVOManagementClusterMetricsAccessEnvVar) == "1" {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  config.EnableCVOManagementClusterMetricsAccessEnvVar,
					Value: "1",
				},
			)
		}

		if managedServiceType, ok := os.LookupEnv(managedServiceEnvVar); ok {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  managedServiceEnvVar,
					Value: managedServiceType,
				},
			)
		}

		if len(cpo.DefaultIngressDomain) > 0 {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  config.DefaultIngressDomainEnvVar,
					Value: cpo.DefaultIngressDomain,
				},
			)
		}
	})

	if hcp.Spec.AdditionalTrustBundle != nil {
		// Add trusted-ca mount with optional configmap
		util.DeploymentAddTrustBundleVolume(hcp.Spec.AdditionalTrustBundle, deployment)
	}

	cpo.applyPlatformSpecificConfig(hcp, deployment)

	return nil
}

func (cpo *ControlPlaneOperatorOptions) applyPlatformSpecificConfig(hcp *hyperv1.HostedControlPlane, deployment *appsv1.Deployment) {
	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "control-plane-operator-creds",
					},
				},
			})
		deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: "/etc/provider/credentials",
			},
			corev1.EnvVar{
				Name:  "AWS_REGION",
				Value: hcp.Spec.Platform.AWS.Region,
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			})
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "provider-creds",
				MountPath: "/etc/provider",
			})
	case hyperv1.AzurePlatform:
		// Add the client ID of the managed Azure key vault as an environment variable on the CPO. This is used in
		// configuring the SecretProviderClass CRs for OpenShift components on the HCP needing to authenticate with
		// Azure cloud API.
		aroHCPKVMIClientID, ok := os.LookupEnv(config.AROHCPKeyVaultManagedIdentityClientID)
		if ok {
			deployment.Spec.Template.Spec.Containers[0].Env = append(deployment.Spec.Template.Spec.Containers[0].Env,
				corev1.EnvVar{
					Name:  config.AROHCPKeyVaultManagedIdentityClientID,
					Value: aroHCPKVMIClientID,
				})
		}

		// Mount the control plane operator's credentials from the managed Azure key vault. The CPO authenticates with
		// the Azure cloud API for validating resource group locations.
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
			azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureCPOSecretStoreVolumeName),
		)
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureCPOSecretStoreVolumeName, config.ManagedAzureCPOSecretProviderClassName),
		)

		// Mount the KMS credentials so the HCP reconciliation can validate the Azure KMS configuration.
		if hcp.Spec.SecretEncryption != nil && hcp.Spec.SecretEncryption.KMS != nil {
			deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
				azureutil.CreateVolumeMountForKMSAzureSecretStoreProviderClass(config.ManagedAzureKMSSecretStoreVolumeName),
			)
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
				azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureKMSSecretStoreVolumeName, config.ManagedAzureKMSSecretProviderClassName),
			)
		}
	}
}
