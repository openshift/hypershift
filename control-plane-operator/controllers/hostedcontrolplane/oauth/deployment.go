package oauth

import (
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
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
		"app": "oauth-openshift",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, image string, deploymentConfig config.DeploymentConfig) error {
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
	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: pointer.BoolPtr(false),
		Containers: []corev1.Container{
			util.BuildContainer(oauthContainerMain(), buildOAuthContainerMain(image)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(oauthVolumeConfig(), buildOAuthVolumeConfig),
			util.BuildVolume(oauthVolumeKubeconfig(), buildOAuthVolumeKubeconfig),
			util.BuildVolume(oauthVolumeServingCert(), buildOAuthVolumeServingCert),
			util.BuildVolume(oauthVolumeSessionSecret(), buildOAuthVolumeSessionSecret),
			util.BuildVolume(oauthVolumeErrorTemplate(), buildOAuthVolumeErrorTemplate),
			util.BuildVolume(oauthVolumeLoginTemplate(), buildOAuthVolumeLoginTemplate),
			util.BuildVolume(oauthVolumeProvidersTemplate(), buildOAuthVolumeProvidersTemplate),
			util.BuildVolume(oauthVolumeWorkLogs(), buildOAuthVolumeWorkLogs),
		},
	}
	deploymentConfig.ApplyTo(deployment)
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
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.WorkingDir = volumeMounts.Path(c.Name, oauthVolumeWorkLogs().Name)
	}
}

func oauthVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-config",
	}
}

func buildOAuthVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.OAuthServerConfig("").Name,
		},
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
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}
func oauthVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOAuthVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.IngressCert("").Name,
	}
}
func oauthVolumeSessionSecret() *corev1.Volume {
	return &corev1.Volume{
		Name: "session-secret",
	}
}
func buildOAuthVolumeSessionSecret(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.OAuthServerServiceSessionSecret("").Name,
	}
}
func oauthVolumeErrorTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "error-template",
	}
}

func buildOAuthVolumeErrorTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.OAuthServerDefaultErrorTemplateSecret("").Name,
	}
}

func oauthVolumeLoginTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "login-template",
	}
}

func buildOAuthVolumeLoginTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.OAuthServerDefaultLoginTemplateSecret("").Name,
	}
}

func oauthVolumeProvidersTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "providers-template",
	}
}

func buildOAuthVolumeProvidersTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.OAuthServerDefaultProviderSelectionTemplateSecret("").Name,
	}
}
