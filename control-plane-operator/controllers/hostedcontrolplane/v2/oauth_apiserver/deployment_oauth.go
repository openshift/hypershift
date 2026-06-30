package oapi

import (
	"fmt"
	"path"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	auditWebhookConfigFileVolumeName = "oauth-audit-webhook"

	auditLogTailScript = `set -o errexit
set -o nounset
set -o pipefail

function cleanup() {
  pkill -P $$$
  wait
  exit
}
trap cleanup SIGTERM

/usr/bin/tail -c+1 -F /var/log/openshift-oauth-apiserver/audit.log &
wait $!`
)

func adaptForOAuth(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	var err error
	configuration := cpContext.HCP.Spec.Configuration

	etcdHostname := "etcd-client"
	if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		etcdHostname, err = netutil.HostFromURL(cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint)
		if err != nil {
			return err
		}
	}

	noProxy := []string{
		manifests.KubeAPIServerService("").Name,
		etcdHostname,
		config.AuditWebhookService,
	}

	etcdURL := config.DefaultEtcdURL
	if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		etcdURL = cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append([]string{
			"start",
			"--authorization-kubeconfig=/etc/kubernetes/secrets/svc-kubeconfig/kubeconfig",
			"--authentication-kubeconfig=/etc/kubernetes/secrets/svc-kubeconfig/kubeconfig",
			"--kubeconfig=/etc/kubernetes/secrets/svc-kubeconfig/kubeconfig",
			"--secure-port=8443",
			"--audit-log-path=/var/log/openshift-oauth-apiserver/audit.log",
			"--audit-log-format=json",
			"--audit-log-maxsize=10",
			"--audit-log-maxbackup=1",
			"--etcd-cafile=/etc/kubernetes/certs/etcd-client-ca/ca.crt",
			"--etcd-keyfile=/etc/kubernetes/certs/etcd-client/etcd-client.key",
			"--etcd-certfile=/etc/kubernetes/certs/etcd-client/etcd-client.crt",
			"--shutdown-delay-duration=15s",
			"--tls-private-key-file=/etc/kubernetes/certs/serving/tls.key",
			"--tls-cert-file=/etc/kubernetes/certs/serving/tls.crt",
			"--audit-policy-file=/etc/kubernetes/audit-config/policy.yaml",
			"--cors-allowed-origins='//127\\.0\\.0\\.1(:|$)'",
			"--cors-allowed-origins='//localhost(:|$)'",
			"--v=2",
			"--requestheader-client-ca-file=/etc/kubernetes/certs/aggregator-client-ca/ca.crt",
			"--requestheader-allowed-names=kube-apiserver-proxy,system:kube-apiserver-proxy,system:openshift-aggregator",
			"--requestheader-username-headers=X-Remote-User",
			"--requestheader-group-headers=X-Remote-Group",
			"--requestheader-extra-headers-prefix=X-Remote-Extra-",
			"--client-ca-file=/etc/kubernetes/certs/client-ca/ca.crt",
			fmt.Sprintf("--api-audiences=%s", cpContext.HCP.Spec.IssuerURL),
			fmt.Sprintf("--etcd-servers=%s", etcdURL),
			fmt.Sprintf("--tls-min-version=%s", config.MinTLSVersion(configuration.GetTLSSecurityProfile())),
		}, c.Args...)

		if cipherSuites := config.CipherSuites(configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

		if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--audit-webhook-config-file=%s", path.Join("/etc/kubernetes/auditwebhook", hyperv1.AuditWebhookKubeconfigKey)))
			c.Args = append(c.Args, "--audit-webhook-mode=batch")
			c.Args = append(c.Args, "--audit-webhook-initial-backoff=5s")
		}

		if configuration != nil && configuration.OAuth != nil && configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout != nil {
			tokenInactivityTimeout := configuration.OAuth.TokenConfig.AccessTokenInactivityTimeout.Duration.String()
			c.Args = append(c.Args, fmt.Sprintf("--accesstoken-inactivity-timeout=%s", tokenInactivityTimeout))
		}

		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: strings.Join(noProxy, ","),
		})

		c.WorkingDir = "/var/log/openshift-oauth-apiserver"
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "healthz",
					Port:   intstr.FromInt32(8443),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			TimeoutSeconds:      1,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "readyz",
					Port:   intstr.FromInt32(8443),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds:    10,
			TimeoutSeconds:   1,
			SuccessThreshold: 1,
			FailureThreshold: 10,
		}
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{Name: "aggregator-ca", MountPath: "/etc/kubernetes/certs/aggregator-client-ca"},
			corev1.VolumeMount{Name: "audit-config", MountPath: "/etc/kubernetes/audit-config"},
			corev1.VolumeMount{Name: "client-ca", MountPath: "/etc/kubernetes/certs/client-ca"},
			corev1.VolumeMount{Name: "etcd-client-ca", MountPath: "/etc/kubernetes/certs/etcd-client-ca"},
			corev1.VolumeMount{Name: "etcd-client-cert", MountPath: "/etc/kubernetes/certs/etcd-client"},
			corev1.VolumeMount{Name: "kubeconfig", MountPath: "/etc/kubernetes/secrets/svc-kubeconfig"},
			corev1.VolumeMount{Name: "work-logs", MountPath: "/var/log/openshift-oauth-apiserver"},
		)
	})

	if cpContext.HCP.Spec.Configuration.GetAuditPolicyConfig().Profile != configv1.NoneAuditProfileType {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:            "audit-logs",
			Image:           "cli",
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/bin/bash"},
			Args:            []string{"-c", auditLogTailScript},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("5m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				corev1.VolumeMount{Name: "work-logs", MountPath: "/var/log/openshift-oauth-apiserver"},
			},
		})
	}

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "work-logs",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "audit-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "openshift-oauth-apiserver-audit"},
					DefaultMode:          ptr.To(int32(420)),
				},
			},
		},
		corev1.Volume{
			Name: "aggregator-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "aggregator-client-ca"},
					DefaultMode:          ptr.To(int32(420)),
				},
			},
		},
		corev1.Volume{
			Name: "etcd-client-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-ca"},
					DefaultMode:          ptr.To(int32(420)),
				},
			},
		},
		corev1.Volume{
			Name: "kubeconfig",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "service-network-admin-kubeconfig",
					DefaultMode: ptr.To(int32(416)),
				},
			},
		},
		corev1.Volume{
			Name: "etcd-client-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "etcd-client-tls",
					DefaultMode: ptr.To(int32(416)),
				},
			},
		},
		corev1.Volume{
			Name: "client-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "client-ca"},
					DefaultMode:          ptr.To(int32(420)),
				},
			},
		},
	)

	if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
		applyAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, cpContext.HCP.Spec.AuditWebhook)
	}

	kasLivezURL := kas.InClusterKASURL(cpContext.HCP.Spec.Platform.Type) + "/livez"
	deployment.Spec.Template.Spec.Containers = append(
		deployment.Spec.Template.Spec.Containers,
		podspec.KASReadinessCheckContainer(kasLivezURL),
	)

	return nil
}

func applyAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: auditWebhookConfigFileVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: auditWebhookRef.Name},
		},
	})

	podspec.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      auditWebhookConfigFileVolumeName,
			MountPath: "/etc/kubernetes/auditwebhook",
		})
	})
}
