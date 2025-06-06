package azure

import (
	"fmt"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerName = "cloud-controller-manager"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(containerName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			fmt.Sprintf("--cluster-name=%s", cpContext.HCP.Spec.InfraID),
		)
		if azureutil.IsAroHCP() {
			c.VolumeMounts = append(c.VolumeMounts,
				azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName),
			)
		}
	})

	if azureutil.IsAroHCP() {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName, config.ManagedAzureCloudProviderSecretProviderClassName),
		)
	}
	return nil
}
