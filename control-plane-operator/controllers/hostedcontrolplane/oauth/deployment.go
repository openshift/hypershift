package oauth

import (
	"context"
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	configHashAnnotation = "oauth.hypershift.openshift.io/config-hash"
)

var (
	volumeMounts = util.PodVolumeMounts{
		oauthContainerMain().Name: {
			oauthVolumeConfig().Name:            "/etc/kubernetes/config",
			oauthVolumeKubeconfig().Name:        "/etc/kubernetes/secrets/svc-kubeconfig",
			oauthVolumeServingCert().Name:       "/etc/kubernetes/certs/serving-cert",
			oauthVolumeSessionSecret().Name:     "/etc/kubernetes/secrets/session",
			oauthVolumeErrorTemplate().Name:     "/etc/kubernetes/secrets/templates/error",
			oauthVolumeLoginTemplate().Name:     "/etc/kubernetes/secrets/templates/login",
			oauthVolumeProvidersTemplate().Name: "/etc/kubernetes/secrets/templates/providers",
			oauthVolumeWorkLogs().Name:          "/var/run/kubernetes",
		},
	}
	oauthLabels = map[string]string{
		"app":                         "oauth-openshift",
		hyperv1.ControlPlaneComponent: "oauth-openshift",
	}
)

func ReconcileDeployment(ctx context.Context, client client.Client, deployment *appsv1.Deployment, ownerRef config.OwnerRef, config *corev1.ConfigMap, image string, deploymentConfig config.DeploymentConfig, identityProviders []configv1.IdentityProvider, providerOverrides map[string]*ConfigOverride, availabilityProberImage string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: oauthLabels,
	}
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	for k, v := range oauthLabels {
		deployment.Spec.Template.ObjectMeta.Labels[k] = v
	}
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	configBytes, ok := config.Data[OAuthServerConfigKey]
	if !ok {
		return fmt.Errorf("oauth server: configuration not found in configmap")
	}
	deployment.Spec.Template.ObjectMeta.Annotations[configHashAnnotation] = util.ComputeHash(configBytes)
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = pointer.BoolPtr(false)
	deployment.Spec.Template.Spec.Containers = util.ApplyContainer(deployment.Spec.Template.Spec.Containers, oauthContainerMain(), buildOAuthContainerMain(image))
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeConfig(), buildOAuthVolumeConfig)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeKubeconfig(), buildOAuthVolumeKubeconfig)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeServingCert(), buildOAuthVolumeServingCert)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeSessionSecret(), buildOAuthVolumeSessionSecret)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeErrorTemplate(), buildOAuthVolumeErrorTemplate)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeLoginTemplate(), buildOAuthVolumeLoginTemplate)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeProvidersTemplate(), buildOAuthVolumeProvidersTemplate)
	deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, oauthVolumeWorkLogs(), buildOAuthVolumeWorkLogs)

	deploymentConfig.ApplyTo(deployment)
	if len(identityProviders) > 0 {
		_, volumeMountInfo, err := convertIdentityProviders(ctx, identityProviders, providerOverrides, client, deployment.Namespace)
		if err != nil {
			return err
		}
		for _, v := range volumeMountInfo.Volumes {
			deployment.Spec.Template.Spec.Volumes = util.ApplyVolume(deployment.Spec.Template.Spec.Volumes, &v, func(volume *corev1.Volume) {})
		}
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = util.ApplyVolumeMount(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, volumeMountInfo.VolumeMounts.ContainerMounts(oauthContainerMain().Name)...)
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func oauthContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-controller-manager",
	}
}

func buildOAuthContainerMain(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Args = []string{
			"osinserver",
			fmt.Sprintf("--config=%s", path.Join(volumeMounts.Path(c.Name, oauthVolumeConfig().Name), OAuthServerConfigKey)),
		}
		c.VolumeMounts = util.ApplyVolumeMount(c.VolumeMounts, volumeMounts.ContainerMounts(c.Name)...)
		c.WorkingDir = volumeMounts.Path(c.Name, oauthVolumeWorkLogs().Name)
	}
}

func oauthVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-config",
	}
}

func buildOAuthVolumeConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.LocalObjectReference = corev1.LocalObjectReference{
		Name: manifests.OAuthServerConfig("").Name,
	}
}

func oauthVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "logs",
	}
}

func buildOAuthVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func oauthVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildOAuthVolumeKubeconfig(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
}
func oauthVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOAuthVolumeServingCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.OpenShiftOAuthServerCert("").Name
}
func oauthVolumeSessionSecret() *corev1.Volume {
	return &corev1.Volume{
		Name: "session-secret",
	}
}
func buildOAuthVolumeSessionSecret(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.OAuthServerServiceSessionSecret("").Name
}
func oauthVolumeErrorTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "error-template",
	}
}

func buildOAuthVolumeErrorTemplate(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.OAuthServerDefaultErrorTemplateSecret("").Name
}

func oauthVolumeLoginTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "login-template",
	}
}

func buildOAuthVolumeLoginTemplate(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.OAuthServerDefaultLoginTemplateSecret("").Name
}

func oauthVolumeProvidersTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "providers-template",
	}
}

func buildOAuthVolumeProvidersTemplate(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.SecretName = manifests.OAuthServerDefaultProviderSelectionTemplateSecret("").Name
}
