package storage

import (
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		envReplacer := newEnvironmentReplacer(cpContext.ReleaseImageProvider, cpContext.UserReleaseImageProvider)
		envReplacer.replaceEnvVars(c.Env)

		// For managed Azure, we need to supply a couple of environment variables for CSO to pass on to the CSI controllers for disk and file.
		// CSO passes those on to the CSI deployment here - https://github.com/openshift/cluster-storage-operator/pull/517/files.
		// CSI then mounts the Secrets Provider Class here - https://github.com/openshift/csi-operator/pull/309/files.
		if azureutil.IsAroHCP() {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK",
					Value: config.ManagedAzureDiskCSISecretStoreProviderClassName,
				},
				corev1.EnvVar{
					Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE",
					Value: config.ManagedAzureFileCSISecretStoreProviderClassName,
				})
		}
	})

	return nil
}
