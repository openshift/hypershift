package openstack

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/config"
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

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, p *OpenStackParams, serviceAccountName string, releaseImageProvider *imageprovider.ReleaseImageProvider) error {
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
					util.BuildContainer(ccmContainer(), buildCCMContainer(releaseImageProvider.GetImage("openstack-cloud-controller-manager"))),
				},
				Volumes:            []corev1.Volume{},
				ServiceAccountName: serviceAccountName,
			},
		},
	}

	addVolumes(deployment)

	if hcp.Spec.Platform.OpenStack.CACertSecret != nil {
		addCACert(deployment)
	}

	config.OwnerRefFrom(hcp).ApplyTo(deployment)
	p.DeploymentConfig.ApplyTo(deployment)
	return nil
}

func addVolumes(deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeKubeconfig(), buildCCMVolumeKubeconfig),
		util.BuildVolume(ccmCloudConfig(), buildCCMCloudConfig),
	)
}

func addCACert(deployment *appsv1.Deployment) {
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmCloudCA(), buildCCMTrustedCA),
	)

	ccmContainer := &deployment.Spec.Template.Spec.Containers[0]
	ccmContainer.VolumeMounts = append(ccmContainer.VolumeMounts, corev1.VolumeMount{
		Name:      ccmCloudCA().Name,
		MountPath: "/etc/pki/ca-trust/extracted/pem",
		ReadOnly:  true,
	})
}

func buildCCMContainer(controllerManagerImage string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = controllerManagerImage
		c.Command = []string{"/usr/bin/openstack-cloud-controller-manager"}
		c.Args = []string{
			"--v=1",
			"--cloud-config=/etc/openstack/config/" + CloudConfigKey,
			"--kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
			"--cloud-provider=openstack",
			"--use-service-account-credentials=false",
			"--configure-cloud-routes=false",
			"--bind-address=127.0.0.1",
			"--leader-elect=true",
			fmt.Sprintf("--leader-elect-lease-duration=%s", config.RecommendedLeaseDuration),
			fmt.Sprintf("--leader-elect-renew-deadline=%s", config.RecommendedRenewDeadline),
			fmt.Sprintf("--leader-elect-retry-period=%s", config.RecommendedRetryPeriod),
			"--leader-elect-resource-namespace=openshift-cloud-controller-manager",
		}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      ccmVolumeKubeconfig().Name,
				MountPath: "/etc/kubernetes/kubeconfig",
				ReadOnly:  true,
			},
			{
				Name:      ccmCloudConfig().Name,
				MountPath: "/etc/openstack/config",
				ReadOnly:  true,
			},
		}
	}
}

func buildCCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildCCMCloudConfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.OpenStackProviderConfig("").Name,
	}
}

func buildCCMTrustedCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: manifests.OpenStackTrustedCA("").Name},
		Items: []corev1.KeyToPath{
			{
				Key:  "ca.pem",
				Path: "ca.pem",
			},
		},
	}
}

func ccmLabels() map[string]string {
	return map[string]string{
		"k8s-app": "openstack-cloud-controller-manager",
		"infrastructure.openshift.io/cloud-controller-manager": "OpenStack",
	}
}
