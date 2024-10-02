package openstack

import (
	"fmt"

	k8sutilspointer "k8s.io/utils/pointer"

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

const (
	secretOCCMVolumeName = "secret-occm"
)

func ReconcileCCMServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
	return nil
}

func ReconcileDeployment(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, p *OpenStackParams, serviceAccountName string, releaseImageProvider imageprovider.ReleaseImageProvider, hasCACert bool) error {
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
					util.BuildContainer(ccmContainer(), buildCCMContainer(releaseImageProvider.GetImage("openstack-cloud-controller-manager"), hcp.Spec.InfraID)),
				},
				Volumes:                      []corev1.Volume{},
				ServiceAccountName:           serviceAccountName,
				AutomountServiceAccountToken: k8sutilspointer.Bool(false),
			},
		},
	}

	addVolumes(deployment, hcp)

	if hasCACert {
		addCACert(deployment)
	}

	config.OwnerRefFrom(hcp).ApplyTo(deployment)
	p.DeploymentConfig.ApplyTo(deployment)
	return nil
}

func addVolumes(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane) {
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeKubeconfig(), buildCCMVolumeKubeconfig),
		util.BuildVolume(ccmCloudConfig(), buildCCMCloudConfig),
		corev1.Volume{
			Name: secretOCCMVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: hcp.Spec.Platform.OpenStack.IdentityRef.Name,
					Items: []corev1.KeyToPath{{
						Key:  CloudsSecretKey,
						Path: CloudsSecretKey,
					}},
				},
			},
		},
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
		MountPath: CaDir,
		ReadOnly:  true,
	})
}

func buildCCMContainer(controllerManagerImage, infraID string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = controllerManagerImage
		c.Command = []string{"/usr/bin/openstack-cloud-controller-manager"}
		c.Args = []string{
			"--v=1",
			"--cloud-config=$(CLOUD_CONFIG)",
			"--cluster-name=$(OCP_INFRASTRUCTURE_NAME)",
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
		c.Env = []corev1.EnvVar{
			{
				Name:  "CLOUD_CONFIG",
				Value: CloudConfigDir + "/" + CredentialsFile,
			},
			{
				Name:  "OCP_INFRASTRUCTURE_NAME",
				Value: infraID,
			},
		}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      ccmVolumeKubeconfig().Name,
				MountPath: "/etc/kubernetes/kubeconfig",
				ReadOnly:  true,
			},
			{
				Name:      ccmCloudConfig().Name,
				MountPath: CloudConfigDir,
				ReadOnly:  true,
			},
			{
				Name:      secretOCCMVolumeName,
				MountPath: CloudCredentialsDir,
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
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: manifests.OpenStackProviderConfig("").Name},
		Items: []corev1.KeyToPath{
			{
				Key:  CredentialsFile,
				Path: CredentialsFile,
			},
		},
	}
}

func buildCCMTrustedCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: manifests.OpenStackTrustedCA("").Name},
		Items: []corev1.KeyToPath{
			{
				Key:  CABundleKey,
				Path: CABundleKey,
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
