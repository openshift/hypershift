package oapi

import (
	"fmt"
	"path"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	auditWebhookConfigFileVolumeName = "oauth-audit-webhook"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {

	var err error
	etcdHostname := "etcd-client"
	if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		etcdHostname, err = util.HostFromURL(cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint)
		if err != nil {
			return err
		}
	}
	noProxy := []string{
		manifests.KubeAPIServerService("").Name,
		etcdHostname,
		config.AuditWebhookService,
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		etcdURL := config.DefaultEtcdURL
		if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
			etcdURL = cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint
		}

		configuration := cpContext.HCP.Spec.Configuration
		c.Args = append(c.Args,
			fmt.Sprintf("--api-audiences=%s", cpContext.HCP.Spec.IssuerURL),
			fmt.Sprintf("--etcd-servers=%s", etcdURL),
			fmt.Sprintf("--tls-min-version=%s", config.MinTLSVersion(configuration.GetTLSSecurityProfile())),
		)

		if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--audit-webhook-config-file=%s", path.Join("/etc/kubernetes/auditwebhook", hyperv1.AuditWebhookKubeconfigKey)))
			c.Args = append(c.Args, "--audit-webhook-mode=batch")
			c.Args = append(c.Args, "--audit-webhook-initial-backoff=5s")
		}

		if configuration != nil && configuration.OAuth != nil && configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout != nil {
			tokenInactivityTimeout := configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout.Duration.String()
			c.Args = append(c.Args, fmt.Sprintf("--accesstoken-inactivity-timeout=%s", tokenInactivityTimeout))
		}

		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: strings.Join(noProxy, ","),
		})

		// If auditing is none/disabled, remove the audit args from the container
		if !kasv2.AuditEnabledWorkloadContext(cpContext) {
			for _, argToRemove := range []string{
				"--audit-log-path=",
				"--audit-log-format=",
				"--audit-log-maxsize=",
				"--audit-log-maxbackup=",
				"--audit-policy-file=",
			} {
				c.Args = slices.DeleteFunc(c.Args, func(s string) bool {
					return strings.Contains(s, argToRemove)
				})
			}
		}
	})

	if !kasv2.AuditEnabledWorkloadContext(cpContext) {
		util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
		util.RemoveContainerVolumeMount("audit-config", util.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers))
		util.RemovePodVolume("audit-config", &deployment.Spec.Template.Spec)
	}

	if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
		applyAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, cpContext.HCP.Spec.AuditWebhook)
	}

	return nil
}

func applyAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: auditWebhookConfigFileVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: auditWebhookRef.Name},
		},
	})

	util.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      auditWebhookConfigFileVolumeName,
			MountPath: "/etc/kubernetes/auditwebhook",
		})
	})
}
