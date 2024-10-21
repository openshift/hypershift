package azure

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcileCCMServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
	return nil
}

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, p *AzureParams, serviceAccountName string, releaseImageProvider imageprovider.ReleaseImageProvider) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: ccmLabels(),
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ccmLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(ccmContainer(), buildCCMContainer(p, releaseImageProvider.GetImage("azure-cloud-controller-manager"), hcp.Namespace)),
				},
				Volumes:            []corev1.Volume{},
				ServiceAccountName: serviceAccountName,
			},
		},
	}

	addVolumes(deployment)

	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts,
		azureutil.CreateVolumeMountForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName),
	)

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
		azureutil.CreateVolumeForAzureSecretStoreProviderClass(config.ManagedAzureCloudProviderSecretStoreVolumeName, config.ManagedAzureCloudProviderSecretProviderClassName),
	)

	config.OwnerRefFrom(hcp).ApplyTo(deployment)
	p.DeploymentConfig.ApplyTo(deployment)
	return nil
}

func addVolumes(deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeKubeconfig(), buildCCMVolumeKubeconfig),
	)
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmCloudConfig(), buildCCMCloudConfig),
	)
}

func podVolumeMounts() util.PodVolumeMounts {
	return util.PodVolumeMounts{
		ccmContainer().Name: util.ContainerVolumeMounts{
			ccmVolumeKubeconfig().Name: "/etc/kubernetes/kubeconfig",
			ccmCloudConfig().Name:      "/etc/cloud",
		},
	}
}

// TODO (Alberto): move all signature input into params?
func buildCCMContainer(p *AzureParams, controllerManagerImage, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = controllerManagerImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/azure-cloud-controller-manager"}
		c.Args = []string{
			"--cloud-config=/etc/cloud/" + CloudConfigKey,
			"--cloud-provider=azure",
			"--controllers=*,-cloud-node",
			"--configure-cloud-routes=false",
			"--bind-address=127.0.0.1",
			"--cluster-name=" + p.ClusterID,
			"--leader-elect=true",
			"--route-reconciliation-period=10s",
			"--kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
			fmt.Sprintf("--leader-elect-lease-duration=%s", config.RecommendedLeaseDuration),
			fmt.Sprintf("--leader-elect-renew-deadline=%s", config.RecommendedRenewDeadline),
			fmt.Sprintf("--leader-elect-retry-period=%s", config.RecommendedRetryPeriod),
			"--leader-elect-resource-namespace=openshift-cloud-controller-manager",
			"--v=4",
		}
		c.VolumeMounts = podVolumeMounts().ContainerMounts(c.Name)
		proxy.SetEnvVars(&c.Env)
	}
}

func buildCCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildCCMCloudConfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.AzureProviderConfigWithCredentials("").Name,
	}
}

func ccmLabels() map[string]string {
	return map[string]string{
		"app": "cloud-controller-manager",
	}
}

func additionalLabels() map[string]string {
	return map[string]string{
		hyperv1.ControlPlaneComponentLabel: "cloud-controller-manager",
	}
}
