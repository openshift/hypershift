package oauth

import (
	"context"
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	configHashAnnotation                 = "oauth.hypershift.openshift.io/config-hash"
	oauthNamedCertificateMountPathPrefix = "/etc/kubernetes/certs/named"
	socks5ProxyContainerName             = "socks-proxy"
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
			oauthVolumeMasterCABundle().Name:    "/etc/kubernetes/certs/master-ca",
		},
	}
)

func oauthLabels() map[string]string {
	return map[string]string{
		"app":                         "oauth-openshift",
		hyperv1.ControlPlaneComponent: "oauth-openshift",
	}
}

func ReconcileDeployment(ctx context.Context, client client.Client, deployment *appsv1.Deployment, ownerRef config.OwnerRef, config *corev1.ConfigMap, image string, deploymentConfig config.DeploymentConfig, identityProviders []configv1.IdentityProvider, providerOverrides map[string]*ConfigOverride, availabilityProberImage string, apiPort *int32, namedCertificates []configv1.APIServerNamedServingCert, socks5ProxyImage string, noProxy []string) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main oauth container
	mainContainer := util.FindContainer(oauthContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: oauthLabels(),
		}
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
	for k, v := range oauthLabels() {
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
	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: utilpointer.BoolPtr(false),
		Containers: []corev1.Container{
			util.BuildContainer(oauthContainerMain(), buildOAuthContainerMain(image, noProxy)),
			socks5ProxyContainer(socks5ProxyImage),
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
			util.BuildVolume(oauthVolumeMasterCABundle(), buildOAuthVolumeMasterCABundle),
			{Name: "admin-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "service-network-admin-kubeconfig", DefaultMode: utilpointer.Int32Ptr(0640)}}},
			{Name: "konnectivity-proxy-cert", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KonnectivityClientSecret("").Name, DefaultMode: utilpointer.Int32Ptr(0640)}}},
			{Name: "konnectivity-proxy-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.KonnectivityCAConfigMap("").Name}, DefaultMode: utilpointer.Int32Ptr(0640)}}},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	if len(identityProviders) > 0 {
		_, volumeMountInfo, err := convertIdentityProviders(ctx, identityProviders, providerOverrides, client, deployment.Namespace)
		if err != nil {
			return err
		}
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumeMountInfo.Volumes...)
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, volumeMountInfo.VolumeMounts.ContainerMounts(oauthContainerMain().Name)...)
	}
	globalconfig.ApplyNamedCertificateMounts(oauthContainerMain().Name, oauthNamedCertificateMountPathPrefix, namedCertificates, &deployment.Spec.Template.Spec)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func oauthContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "oauth-server",
	}
}

func buildOAuthContainerMain(image string, noProxy []string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Args = []string{
			"osinserver",
			fmt.Sprintf("--config=%s", path.Join(volumeMounts.Path(c.Name, oauthVolumeConfig().Name), OAuthServerConfigKey)),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.WorkingDir = volumeMounts.Path(c.Name, oauthVolumeWorkLogs().Name)
		c.Env = []corev1.EnvVar{
			{
				Name:  "HTTP_PROXY",
				Value: fmt.Sprintf("socks5://127.0.0.1:%d", konnectivity.KonnectivityServerLocalPort),
			},
			{
				Name:  "HTTPS_PROXY",
				Value: fmt.Sprintf("socks5://127.0.0.1:%d", konnectivity.KonnectivityServerLocalPort),
			},
			{
				Name:  "ALL_PROXY",
				Value: fmt.Sprintf("socks5://127.0.0.1:%d", konnectivity.KonnectivityServerLocalPort),
			},
			{
				Name:  "NO_PROXY",
				Value: strings.Join(noProxy, ","),
			},
		}
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
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.KASServiceKubeconfigSecret("").Name,
	}
}
func oauthVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOAuthVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.OpenShiftOAuthServerCert("").Name,
	}
}
func oauthVolumeSessionSecret() *corev1.Volume {
	return &corev1.Volume{
		Name: "session-secret",
	}
}
func buildOAuthVolumeSessionSecret(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.OAuthServerServiceSessionSecret("").Name,
	}
}
func oauthVolumeErrorTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "error-template",
	}
}

func buildOAuthVolumeErrorTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.OAuthServerDefaultErrorTemplateSecret("").Name,
	}
}

func oauthVolumeLoginTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "login-template",
	}
}

func buildOAuthVolumeLoginTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.OAuthServerDefaultLoginTemplateSecret("").Name,
	}
}

func oauthVolumeProvidersTemplate() *corev1.Volume {
	return &corev1.Volume{
		Name: "providers-template",
	}
}

func buildOAuthVolumeProvidersTemplate(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		DefaultMode: utilpointer.Int32Ptr(0640),
		SecretName:  manifests.OAuthServerDefaultProviderSelectionTemplateSecret("").Name,
	}
}

func oauthVolumeMasterCABundle() *corev1.Volume {
	return &corev1.Volume{
		Name: "master-ca-bundle",
	}
}

func buildOAuthVolumeMasterCABundle(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.OpenShiftOAuthMasterCABundle("").Name
}

func socks5ProxyContainer(socks5ProxyImage string) corev1.Container {
	c := corev1.Container{
		Name:    socks5ProxyContainerName,
		Image:   socks5ProxyImage,
		Command: []string{"/usr/bin/control-plane-operator", "konnectivity-socks5-proxy", "--resolve-from-guest-cluster-dns=true", "--resolve-from-management-cluster-dns=true"},
		Args:    []string{"run"},
		Env: []corev1.EnvVar{{
			Name:  "KUBECONFIG",
			Value: "/etc/kubernetes/kubeconfig",
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "admin-kubeconfig", MountPath: "/etc/kubernetes"},
			{Name: "konnectivity-proxy-cert", MountPath: "/etc/konnectivity/proxy-client"},
			{Name: "konnectivity-proxy-ca", MountPath: "/etc/konnectivity/proxy-ca"},
		},
	}

	return c
}
