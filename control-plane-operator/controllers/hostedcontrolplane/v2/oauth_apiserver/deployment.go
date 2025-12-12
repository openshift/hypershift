package oapi

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	auditWebhookConfigFileVolumeName = "oauth-audit-webhook"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {

	var err error
	etcdHostname := "etcd-client"
	etcdURL := config.DefaultEtcdURL

	if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		etcdHostname, err = util.HostFromURL(cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint)
		if err != nil {
			return err
		}
		etcdURL = cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint
	} else {
		// For managed etcd, determine the URL based on sharding configuration
		if len(cpContext.HCP.Spec.Etcd.Managed.Shards) > 0 {
			// When sharded, find the default shard (one containing "/" prefix)
			namespace := cpContext.HCP.Namespace
			for _, shard := range cpContext.HCP.Spec.Etcd.Managed.Shards {
				for _, prefix := range shard.ResourcePrefixes {
					if prefix == "/" {
						// Found the default shard
						serviceName := fmt.Sprintf("etcd-client-%s", shard.Name)
						etcdURL = fmt.Sprintf("https://%s.%s.svc:2379", serviceName, namespace)
						etcdHostname = serviceName
						break
					}
				}
			}
		}
		// If not sharded or no default shard found, use the default etcd-client service
		// etcdURL and etcdHostname are already set to defaults
	}

	// For NO_PROXY, we need both the short hostname and the FQDN that's actually used in the URL
	// The FQDN is extracted from the etcdURL (e.g., etcd-client-main.clusters-etcd-shard-test.svc)
	etcdFQDN := etcdHostname
	if etcdURL != config.DefaultEtcdURL && cpContext.HCP.Spec.Etcd.ManagementType != hyperv1.Unmanaged {
		// Extract the host part from the URL (everything between https:// and :2379)
		if hostStart := len("https://"); len(etcdURL) > hostStart {
			if portIdx := strings.Index(etcdURL[hostStart:], ":"); portIdx > 0 {
				etcdFQDN = etcdURL[hostStart : hostStart+portIdx]
			}
		}
	}

	noProxy := []string{
		manifests.KubeAPIServerService("").Name,
		etcdHostname,       // Short name for backwards compatibility
		etcdFQDN,          // FQDN that's actually used in connections
		config.AuditWebhookService,
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
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
	})

	if cpContext.HCP.Spec.Configuration.GetAuditPolicyConfig().Profile == configv1.NoneAuditProfileType {
		util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
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
