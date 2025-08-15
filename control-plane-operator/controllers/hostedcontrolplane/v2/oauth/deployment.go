package oauth

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	oauthNamedCertificateMountPathPrefix = "/etc/kubernetes/certs/named"

	oauthErrorTemplateVolumeName     = "error-template"
	oauthLoginTemplateVolumeName     = "login-template"
	oauthProvidersTemplateVolumeName = "providers-template"
	auditWebhookConfigFileVolumeName = "oauth-audit-webhook"

	KubeadminSecretHashAnnotation = "hypershift.openshift.io/kubeadmin-secret-hash"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--audit-webhook-config-file=%s", path.Join("/etc/kubernetes/auditwebhook", hyperv1.AuditWebhookKubeconfigKey)))
			c.Args = append(c.Args, "--audit-webhook-mode=batch")
			c.Args = append(c.Args, "--audit-webhook-initial-backoff=5s")

			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      auditWebhookConfigFileVolumeName,
				MountPath: "/etc/kubernetes/auditwebhook",
			})

			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: auditWebhookConfigFileVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: cpContext.HCP.Spec.AuditWebhook.Name},
				},
			})
		}

		if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			noProxy := []string{
				manifests.KubeAPIServerService("").Name, config.AuditWebhookService,
				"iam.cloud.ibm.com", "iam.test.cloud.ibm.com",
			}
			util.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "NO_PROXY",
				Value: strings.Join(noProxy, ","),
			})
		}
	})

	configuration := cpContext.HCP.Spec.Configuration
	if configuration.GetAuditPolicyConfig().Profile == configv1.NoneAuditProfileType {
		util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
	}

	if namedCertificates := configuration.GetNamedCertificates(); len(namedCertificates) > 0 {
		applyNamedCertificateMounts(namedCertificates, &deployment.Spec.Template.Spec)
	}

	if configuration != nil && configuration.OAuth != nil {
		oauthTemplates := configuration.OAuth.Templates
		if oauthTemplates.Error.Name != "" {
			util.UpdateVolume(oauthErrorTemplateVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
				v.Secret.SecretName = oauthTemplates.Error.Name
			})
		}
		if oauthTemplates.Login.Name != "" {
			util.UpdateVolume(oauthLoginTemplateVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
				v.Secret.SecretName = oauthTemplates.Login.Name
			})
		}
		if oauthTemplates.ProviderSelection.Name != "" {
			util.UpdateVolume(oauthProvidersTemplateVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
				v.Secret.SecretName = oauthTemplates.ProviderSelection.Name
			})
		}

		identityProviders := configuration.OAuth.IdentityProviders
		if len(identityProviders) > 0 {
			_, volumeMountInfo, _ := ConvertIdentityProviders(cpContext, identityProviders, providerOverrides(cpContext.HCP), cpContext.Client, deployment.Namespace)
			// Ignore the error here, since we don't want to fail the deployment if the identity providers are invalid
			// A condition will be set on the HC to indicate the error
			if len(volumeMountInfo.Volumes) > 0 {
				deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumeMountInfo.Volumes...)
				util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
					c.VolumeMounts = append(c.VolumeMounts, volumeMountInfo.VolumeMounts.ContainerMounts(ComponentName)...)
				})
			}
		}
	}

	kubeadminPasswordSecret := common.KubeadminPasswordSecret(deployment.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get kubeadmin password secret: %v", err)
		}
		delete(deployment.Spec.Template.Annotations, KubeadminSecretHashAnnotation)
	} else {
		if deployment.Spec.Template.Annotations == nil {
			deployment.Spec.Template.Annotations = map[string]string{}
		}
		deployment.Spec.Template.Annotations[KubeadminSecretHashAnnotation] = kubeadminPasswordSecret.Annotations[KubeadminSecretHashAnnotation]
	}

	return nil
}

func applyNamedCertificateMounts(certs []configv1.APIServerNamedServingCert, spec *corev1.PodSpec) {
	util.UpdateContainer(ComponentName, spec.Containers, func(c *corev1.Container) {
		for i, namedCert := range certs {
			volumeName := fmt.Sprintf("named-cert-%d", i+1)
			spec.Volumes = append(spec.Volumes, corev1.Volume{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  namedCert.ServingCertificate.Name,
						DefaultMode: ptr.To[int32](0640),
					},
				},
			})

			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      volumeName,
				MountPath: fmt.Sprintf("%s-%d", oauthNamedCertificateMountPathPrefix, i+1),
			})
		}
	})
}
