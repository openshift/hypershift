package oapi

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

var (
	oauthVolumeMounts = util.PodVolumeMounts{
		oauthContainerMain().Name: {
			oauthVolumeWorkLogs().Name:           "/var/log/openshift-apiserver",
			oauthVolumeAuditConfig().Name:        "/etc/kubernetes/audit-config",
			oauthVolumeAggregatorClientCA().Name: "/etc/kubernetes/certs/aggregator-client-ca",
			oauthVolumeEtcdClientCA().Name:       "/etc/kubernetes/certs/etcd-client-ca",
			oauthVolumeServingCA().Name:          "/etc/kubernetes/certs/serving-ca",
			oauthVolumeKubeconfig().Name:         "/etc/kubernetes/secrets/svc-kubeconfig",
			oauthVolumeServingCert().Name:        "/etc/kubernetes/certs/serving",
			oauthVolumeEtcdClientCert().Name:     "/etc/kubernetes/certs/etcd-client",
		},
	}
)

func openShiftOAuthAPIServerLabels() map[string]string {
	return map[string]string{
		"app":                         "openshift-oauth-apiserver",
		hyperv1.ControlPlaneComponent: "openshift-oauth-apiserver",
	}
}

func ReconcileOAuthAPIServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, p *OAuthDeploymentParams, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main oauth apiserver container
	mainContainer := util.FindContainer(oauthContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		p.DeploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	maxUnavailable := intstr.FromInt(1)
	maxSurge := intstr.FromInt(3)

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
	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: pointer.BoolPtr(false),
		Containers: []corev1.Container{
			util.BuildContainer(oauthContainerMain(), buildOAuthContainerMain(p)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(oauthVolumeWorkLogs(), buildOAuthVolumeWorkLogs),
			util.BuildVolume(oauthVolumeAuditConfig(), buildOAuthVolumeAuditConfig),
			util.BuildVolume(oauthVolumeAggregatorClientCA(), buildOAuthVolumeAggregatorClientCA),
			util.BuildVolume(oauthVolumeEtcdClientCA(), buildOAuthVolumeEtcdClientCA),
			util.BuildVolume(oauthVolumeServingCA(), buildOAuthVolumeServingCA),
			util.BuildVolume(oauthVolumeKubeconfig(), buildOAuthVolumeKubeconfig),
			util.BuildVolume(oauthVolumeServingCert(), buildOAuthVolumeServingCert),
			util.BuildVolume(oauthVolumeEtcdClientCert(), buildOAuthVolumeEtcdClientCert),
		},
	}
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), p.AvailabilityProberImage, &deployment.Spec.Template.Spec)
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
			"--audit-log-maxsize=100",
			"--audit-log-maxbackup=10",
			fmt.Sprintf("--etcd-cafile=%s", cpath(oauthVolumeEtcdClientCA().Name, certs.CASignerCertMapKey)),
			fmt.Sprintf("--etcd-keyfile=%s", cpath(oauthVolumeEtcdClientCert().Name, pki.EtcdClientKeyKey)),
			fmt.Sprintf("--etcd-certfile=%s", cpath(oauthVolumeEtcdClientCert().Name, pki.EtcdClientCrtKey)),
			"--shutdown-delay-duration=3s",
			fmt.Sprintf("--tls-private-key-file=%s", cpath(oauthVolumeServingCert().Name, corev1.TLSPrivateKeyKey)),
			fmt.Sprintf("--tls-cert-file=%s", cpath(oauthVolumeServingCert().Name, corev1.TLSCertKey)),
			fmt.Sprintf("--audit-policy-file=%s", cpath(oauthVolumeAuditConfig().Name, auditPolicyConfigMapKey)),
			"--cors-allowed-origins='//127\\.0\\.0\\.1(:|$)'",
			"--cors-allowed-origins='//localhost(:|$)'",
			fmt.Sprintf("--etcd-servers=%s", p.EtcdURL),
			"--v=2",
			fmt.Sprintf("--tls-min-version=%s", p.MinTLSVersion),
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
}

func oauthVolumeAggregatorClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-client-ca",
	}
}

func buildOAuthVolumeAggregatorClientCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.RootCASecret("").Name
}

func oauthVolumeEtcdClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-ca",
	}
}

func buildOAuthVolumeEtcdClientCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.RootCASecret("").Name
}

func oauthVolumeServingCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-ca",
	}
}

func buildOAuthVolumeServingCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.RootCASecret("").Name
}

func oauthVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOAuthVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftOAuthAPIServerCertSecret("").Name
}

func oauthVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-cert",
	}
}

func buildOAuthVolumeEtcdClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.EtcdClientSecret("").Name
}

func ReconcileOpenShiftOAuthAPIServerPodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *OAuthDeploymentParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftOAuthAPIServerLabels(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)

	var minAvailable int
	switch p.Availability {
	case hyperv1.SingleReplica:
		minAvailable = 0
	case hyperv1.HighlyAvailable:
		minAvailable = 1
	}
	pdb.Spec.MinAvailable = &intstr.IntOrString{Type: intstr.Int, IntVal: int32(minAvailable)}

	return nil
}
