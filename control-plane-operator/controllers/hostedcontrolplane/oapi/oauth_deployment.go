package oapi

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	oapiAuditConfigHashAnnotation = "openshift-oauth-apiserver.hypershift.openshift.io/audit-config-hash"
)

var (
	oauthVolumeMounts = util.PodVolumeMounts{
		oauthContainerMain().Name: {
			oauthVolumeWorkLogs().Name:        "/var/log/openshift-oauth-apiserver",
			oauthVolumeAuditConfig().Name:     "/etc/kubernetes/audit-config",
			common.VolumeAggregatorCA().Name:  "/etc/kubernetes/certs/aggregator-client-ca",
			oauthVolumeEtcdClientCA().Name:    "/etc/kubernetes/certs/etcd-client-ca",
			oauthVolumeKubeconfig().Name:      "/etc/kubernetes/secrets/svc-kubeconfig",
			oauthVolumeServingCert().Name:     "/etc/kubernetes/certs/serving",
			oauthVolumeEtcdClientCert().Name:  "/etc/kubernetes/certs/etcd-client",
			common.VolumeTotalClientCA().Name: "/etc/kubernetes/certs/client-ca",
		},
	}
	oauthAuditWebhookConfigFileVolumeMount = util.PodVolumeMounts{
		oauthContainerMain().Name: {
			oauthAuditWebhookConfigFileVolume().Name: "/etc/kubernetes/auditwebhook",
		},
	}
)

func openShiftOAuthAPIServerLabels() map[string]string {
	return map[string]string{
		"app":                              "openshift-oauth-apiserver",
		hyperv1.ControlPlaneComponentLabel: "openshift-oauth-apiserver",
	}
}

func ReconcileOAuthAPIServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, auditConfig *corev1.ConfigMap, p *OAuthDeploymentParams, platformType hyperv1.PlatformType) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main oauth apiserver container
	mainContainer := util.FindContainer(oauthContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		p.DeploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	maxUnavailable := intstr.FromInt(1)
	maxSurge := intstr.FromInt(3)

	auditConfigBytes, ok := auditConfig.Data[auditPolicyConfigMapKey]
	if !ok {
		return fmt.Errorf("openshift-oauth-apiserver audit configuration is not expected to be empty")
	}
	auditConfigHash := util.ComputeHash(auditConfigBytes)

	deployment.Spec.Strategy = appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &maxUnavailable,
			MaxSurge:       &maxSurge,
		},
	}
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftOAuthAPIServerLabels(),
		}
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftOAuthAPIServerLabels()
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations[oapiAuditConfigHashAnnotation] = auditConfigHash

	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken:  ptr.To(false),
		TerminationGracePeriodSeconds: ptr.To[int64](120),
		Containers: []corev1.Container{
			util.BuildContainer(oauthContainerMain(), buildOAuthContainerMain(p)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(oauthVolumeWorkLogs(), buildOAuthVolumeWorkLogs),
			util.BuildVolume(oauthVolumeAuditConfig(), buildOAuthVolumeAuditConfig),
			util.BuildVolume(common.VolumeAggregatorCA(), common.BuildVolumeAggregatorCA),
			util.BuildVolume(oauthVolumeEtcdClientCA(), buildOAuthVolumeEtcdClientCA),
			util.BuildVolume(oauthVolumeKubeconfig(), buildOAuthVolumeKubeconfig),
			util.BuildVolume(oauthVolumeServingCert(), buildOAuthVolumeServingCert),
			util.BuildVolume(oauthVolumeEtcdClientCert(), buildOAuthVolumeEtcdClientCert),
			util.BuildVolume(common.VolumeTotalClientCA(), common.BuildVolumeTotalClientCA),
		},
	}

	if auditConfig.Data[auditPolicyProfileMapKey] != string(configv1.NoneAuditProfileType) {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:            "audit-logs",
			Image:           p.Image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/bin/bash"},
			Args: []string{
				"-c",
				kas.RenderAuditLogScript(fmt.Sprintf("%s/%s", oauthVolumeMounts.Path(oauthContainerMain().Name, oauthVolumeWorkLogs().Name), "audit.log")),
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("5m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      oauthVolumeWorkLogs().Name,
				MountPath: oauthVolumeMounts.Path(oauthContainerMain().Name, oauthVolumeWorkLogs().Name),
			}},
		})
	}

	if p.AuditWebhookRef != nil {
		applyOauthAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, p.AuditWebhookRef)
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), p.AvailabilityProberImage, &deployment.Spec.Template.Spec)
	p.DeploymentConfig.ApplyTo(deployment)
	return nil
}

func oauthContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "oauth-apiserver",
	}
}

func buildOAuthContainerMain(p *OAuthDeploymentParams) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		cpath := func(volume, file string) string {
			return path.Join(oauthVolumeMounts.Path(c.Name, volume), file)
		}
		c.Image = p.Image
		c.Command = []string{"/usr/bin/oauth-apiserver"}
		c.Args = []string{
			"start",
			fmt.Sprintf("--authorization-kubeconfig=%s", cpath(oauthVolumeKubeconfig().Name, kas.KubeconfigKey)),
			fmt.Sprintf("--authentication-kubeconfig=%s", cpath(oauthVolumeKubeconfig().Name, kas.KubeconfigKey)),
			fmt.Sprintf("--kubeconfig=%s", cpath(oauthVolumeKubeconfig().Name, kas.KubeconfigKey)),
			fmt.Sprintf("--secure-port=%d", OpenShiftOAuthAPIServerPort),
			fmt.Sprintf("--api-audiences=%s", p.ServiceAccountIssuerURL),
			fmt.Sprintf("--audit-log-path=%s", cpath(oauthVolumeWorkLogs().Name, "audit.log")),
			"--audit-log-format=json",
			"--audit-log-maxsize=10",
			"--audit-log-maxbackup=1",
			fmt.Sprintf("--etcd-cafile=%s", cpath(oauthVolumeEtcdClientCA().Name, certs.CASignerCertMapKey)),
			fmt.Sprintf("--etcd-keyfile=%s", cpath(oauthVolumeEtcdClientCert().Name, pki.EtcdClientKeyKey)),
			fmt.Sprintf("--etcd-certfile=%s", cpath(oauthVolumeEtcdClientCert().Name, pki.EtcdClientCrtKey)),
			"--shutdown-delay-duration=15s",
			fmt.Sprintf("--tls-private-key-file=%s", cpath(oauthVolumeServingCert().Name, corev1.TLSPrivateKeyKey)),
			fmt.Sprintf("--tls-cert-file=%s", cpath(oauthVolumeServingCert().Name, corev1.TLSCertKey)),
			fmt.Sprintf("--audit-policy-file=%s", cpath(oauthVolumeAuditConfig().Name, auditPolicyConfigMapKey)),
			"--cors-allowed-origins='//127\\.0\\.0\\.1(:|$)'",
			"--cors-allowed-origins='//localhost(:|$)'",
			fmt.Sprintf("--etcd-servers=%s", p.EtcdURL),
			"--v=2",
			fmt.Sprintf("--tls-min-version=%s", p.MinTLSVersion),
			fmt.Sprintf("--requestheader-client-ca-file=%s", cpath(common.VolumeAggregatorCA().Name, certs.CASignerCertMapKey)),
			"--requestheader-allowed-names=kube-apiserver-proxy,system:kube-apiserver-proxy,system:openshift-aggregator",
			"--requestheader-username-headers=X-Remote-User",
			"--requestheader-group-headers=X-Remote-Group",
			"--requestheader-extra-headers-prefix=X-Remote-Extra-",
			fmt.Sprintf("--client-ca-file=%s", cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey)),
		}
		if p.AuditWebhookRef != nil {
			c.Args = append(c.Args, fmt.Sprintf("--audit-webhook-config-file=%s", oauthAuditWebhookConfigFile()))
			c.Args = append(c.Args, "--audit-webhook-mode=batch")
		}
		if p.AccessTokenInactivityTimeout != nil {
			c.Args = append(c.Args, fmt.Sprintf("--accesstoken-inactivity-timeout=%s", p.AccessTokenInactivityTimeout.Duration.String()))
		}
		c.VolumeMounts = oauthVolumeMounts.ContainerMounts(c.Name)
		c.WorkingDir = oauthVolumeMounts.Path(oauthContainerMain().Name, oauthVolumeWorkLogs().Name)
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		}
	}
}

func oauthVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "work-logs",
	}
}

func buildOAuthVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func oauthVolumeAuditConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "audit-config",
	}
}

func buildOAuthVolumeAuditConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.OpenShiftOAuthAPIServerAuditConfig("").Name
}

func oauthVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildOAuthVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}

func oauthVolumeEtcdClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-ca",
	}
}

func buildOAuthVolumeEtcdClientCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.EtcdSignerCAConfigMap("").Name
}

func oauthVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOAuthVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftOAuthAPIServerCertSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}

func oauthVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-cert",
	}
}

func oauthAuditWebhookConfigFileVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-audit-webhook",
	}
}

func buildOauthAuditWebhookConfigFileVolume(auditWebhookRef *corev1.LocalObjectReference) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = auditWebhookRef.Name
	}
}

func applyOauthAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(oauthAuditWebhookConfigFileVolume(), buildOauthAuditWebhookConfigFileVolume(auditWebhookRef)))
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == oauthContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main openshift apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		oauthAuditWebhookConfigFileVolumeMount.ContainerMounts(oauthContainerMain().Name)...)
}
func oauthAuditWebhookConfigFile() string {
	cfgDir := oauthAuditWebhookConfigFileVolumeMount.Path(oauthContainerMain().Name, oauthAuditWebhookConfigFileVolume().Name)
	return path.Join(cfgDir, hyperv1.AuditWebhookKubeconfigKey)
}

func buildOAuthVolumeEtcdClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.EtcdClientSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}

func ReconcileOpenShiftOAuthAPIServerPodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *OAuthDeploymentParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftOAuthAPIServerLabels(),
		}
	}
	p.OwnerRef.ApplyTo(pdb)
	util.ReconcilePodDisruptionBudget(pdb, p.Availability)
	return nil
}
