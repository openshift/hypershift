package aws

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcileCCMServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
	return nil
}

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, serviceAccountName string, releaseImage *releaseinfo.ReleaseImage) error {
	deploymentConfig := newDeploymentConfig()
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
					util.BuildContainer(ccmContainer(), buildCCMContainer(releaseImage.ComponentImages()["aws-cloud-controller-manager"])),
				},
				Volumes:            []corev1.Volume{},
				ServiceAccountName: serviceAccountName,
			},
		},
	}

	addVolumes(deployment)

	util.ApplyCloudProviderCreds(&deployment.Spec.Template.Spec, Provider, &corev1.LocalObjectReference{Name: KubeCloudControllerCredsSecret("").Name}, releaseImage.ComponentImages()["token-minter"], ccmContainer().Name)

	config.OwnerRefFrom(hcp).ApplyTo(deployment)
	deploymentConfig.ApplyTo(deployment)
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
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmCloudControllerCreds(), buildCCMControllerCreds),
	)
}

func podVolumeMounts() util.PodVolumeMounts {
	return util.PodVolumeMounts{
		ccmContainer().Name: util.ContainerVolumeMounts{
			ccmVolumeKubeconfig().Name:     "/etc/kubernetes/kubeconfig",
			ccmCloudConfig().Name:          "/etc/cloud",
			ccmCloudControllerCreds().Name: "/etc/aws",
		},
	}
}

func buildCCMContainer(controllerManagerImage string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = controllerManagerImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/aws-cloud-controller-manager"}
		c.Args = []string{
			"--cloud-provider=aws",
			"--use-service-account-credentials=false",
			"--kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
			"--cloud-config=/etc/cloud/aws.conf",
			"--configure-cloud-routes=false",
			"--leader-elect=true",
			fmt.Sprintf("--leader-elect-lease-duration=%s", config.RecommendedLeaseDuration),
			fmt.Sprintf("--leader-elect-renew-deadline=%s", config.RecommendedRenewDeadline),
			fmt.Sprintf("--leader-elect-retry-period=%s", config.RecommendedRetryPeriod),
			"--leader-elect-resource-namespace=openshift-cloud-controller-manager",
		}
		c.VolumeMounts = podVolumeMounts().ContainerMounts(c.Name)
	}
}

func buildCCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildCCMControllerCreds(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: KubeCloudControllerCredsSecret("").Name,
	}
}

func buildCCMCloudConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.AWSProviderConfig("").Name,
		},
	}
}

func newDeploymentConfig() config.DeploymentConfig {
	result := config.DeploymentConfig{}
	result.Resources = config.ResourcesSpec{
		ccmContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("60Mi"),
				corev1.ResourceCPU:    resource.MustParse("75m"),
			},
		},
	}
	result.AdditionalLabels = additionalLabels()
	result.Scheduling.PriorityClass = config.DefaultPriorityClass

	result.Replicas = 1

	return result
}

func ccmLabels() map[string]string {
	return map[string]string{
		"app": "cloud-controller-manager",
	}
}

func additionalLabels() map[string]string {
	return map[string]string{
		hyperv1.ControlPlaneComponent: "cloud-controller-manager",
	}
}
