package oapi

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	configHashAnnotation = "openshift-apiserver.hypershift.openshift.io/config-hash"

	// defaultOAPIPort is the default secure listen port for the OAPI server
	defaultOAPIPort int32 = 8443
)

var (
	volumeMounts = util.PodVolumeMounts{
		oasTrustAnchorGenerator().Name: {
			oasTrustAnchorVolume().Name:  "/run/ca-trust-generated",
			serviceCASignerVolume().Name: "/run/service-ca-signer",
		},
		oasContainerMain().Name: {
			oasVolumeWorkLogs().Name:          "/var/log/openshift-apiserver",
			oasVolumeConfig().Name:            "/etc/kubernetes/config",
			oasVolumeAuditConfig().Name:       "/etc/kubernetes/audit-config",
			common.VolumeAggregatorCA().Name:  "/etc/kubernetes/certs/aggregator-client-ca",
			oasVolumeEtcdClientCA().Name:      "/etc/kubernetes/certs/etcd-client-ca",
			oasVolumeKubeconfig().Name:        "/etc/kubernetes/secrets/svc-kubeconfig",
			common.VolumeTotalClientCA().Name: "/etc/kubernetes/certs/client-ca",
			oasVolumeServingCert().Name:       "/etc/kubernetes/certs/serving",
			oasVolumeEtcdClientCert().Name:    "/etc/kubernetes/certs/etcd-client",
			oasTrustAnchorVolume().Name:       "/etc/pki/ca-trust/extracted/pem",
			pullSecretVolume().Name:           "/var/lib/kubelet",
		},
		oasSocks5ProxyContainer().Name: {
			oasVolumeKubeconfig().Name:            "/etc/kubernetes/secrets/kubeconfig",
			oasVolumeKonnectivityProxyCert().Name: "/etc/konnectivity/proxy-client",
			oasVolumeKonnectivityProxyCA().Name:   "/etc/konnectivity/proxy-ca",
		},
	}

	oasAuditWebhookConfigFileVolumeMount = util.PodVolumeMounts{
		oasContainerMain().Name: {
			oasAuditWebhookConfigFileVolume().Name: "/etc/kubernetes/auditwebhook",
		},
	}
)

func openShiftAPIServerLabels() map[string]string {
	return map[string]string{
		"app":                         "openshift-apiserver",
		hyperv1.ControlPlaneComponent: "openshift-apiserver",
	}
}

func ReconcileDeployment(deployment *appsv1.Deployment, auditWebhookRef *corev1.LocalObjectReference, ownerRef config.OwnerRef, config *corev1.ConfigMap, deploymentConfig config.DeploymentConfig, image string, socks5ProxyImage string, etcdURL string, availabilityProberImage string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main OAS container
	mainContainer := util.FindContainer(oasContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	configBytes, ok := config.Data[openshiftAPIServerConfigKey]
	if !ok {
		return fmt.Errorf("openshift apiserver configuration is not expected to be empty")
	}
	configHash := util.ComputeHash(configBytes)

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
			MatchLabels: openShiftAPIServerLabels(),
		}
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftAPIServerLabels()
	etcdUrlData, err := url.Parse(etcdURL)
	if err != nil {
		return fmt.Errorf("failed to parse etcd url: %w", err)
	}
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations[configHashAnnotation] = configHash

	deployment.Spec.Template.Spec = corev1.PodSpec{
		AutomountServiceAccountToken: pointer.BoolPtr(false),
		InitContainers:               []corev1.Container{util.BuildContainer(oasTrustAnchorGenerator(), buildOASTrustAnchorGenerator(image))},
		Containers: []corev1.Container{
			util.BuildContainer(oasContainerMain(), buildOASContainerMain(image, strings.Split(etcdUrlData.Host, ":")[0], defaultOAPIPort)),
			{
				Name:            "audit-logs",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command: []string{
					"/usr/bin/tail",
					"-c+1",
					"-F",
					fmt.Sprintf("%s/%s", volumeMounts.Path(oasContainerMain().Name, oasVolumeWorkLogs().Name), "audit.log"),
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5m"),
						corev1.ResourceMemory: resource.MustParse("10Mi"),
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      oasVolumeWorkLogs().Name,
					MountPath: volumeMounts.Path(oasContainerMain().Name, oasVolumeWorkLogs().Name),
				}},
			},
			util.BuildContainer(oasSocks5ProxyContainer(), buildOASSocks5ProxyContainer(socks5ProxyImage)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(oasVolumeWorkLogs(), buildOASVolumeWorkLogs),
			util.BuildVolume(oasVolumeConfig(), buildOASVolumeConfig),
			util.BuildVolume(oasVolumeAuditConfig(), buildOASVolumeAuditConfig),
			util.BuildVolume(common.VolumeAggregatorCA(), common.BuildVolumeAggregatorCA),
			util.BuildVolume(oasVolumeEtcdClientCA(), buildOASVolumeEtcdClientCA),
			util.BuildVolume(common.VolumeTotalClientCA(), common.BuildVolumeTotalClientCA),
			util.BuildVolume(oasVolumeKubeconfig(), buildOASVolumeKubeconfig),
			util.BuildVolume(oasVolumeServingCert(), buildOASVolumeServingCert),
			util.BuildVolume(oasVolumeEtcdClientCert(), buildOASVolumeEtcdClientCert),
			util.BuildVolume(oasVolumeKonnectivityProxyCert(), buildOASVolumeKonnectivityProxyCert),
			util.BuildVolume(oasVolumeKonnectivityProxyCA(), buildOASVolumeKonnectivityProxyCA),
			util.BuildVolume(oasTrustAnchorVolume(), func(v *corev1.Volume) { v.EmptyDir = &corev1.EmptyDirVolumeSource{} }),
			util.BuildVolume(serviceCASignerVolume(), func(v *corev1.Volume) {
				v.ConfigMap = &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.ServiceServingCA(deployment.Namespace).Name}}
			}),
			util.BuildVolume(pullSecretVolume(), func(v *corev1.Volume) {
				v.Secret = &corev1.SecretVolumeSource{
					DefaultMode: pointer.Int32Ptr(0640),
					SecretName:  common.PullSecret(deployment.Namespace).Name,
					Items:       []corev1.KeyToPath{{Key: ".dockerconfigjson", Path: "config.json"}},
				}
			}),
		},
	}

	if auditWebhookRef != nil {
		applyOASAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, auditWebhookRef)
	}

	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)

	deploymentConfig.ApplyTo(deployment)

	return nil
}

func oasTrustAnchorGenerator() *corev1.Container {
	return &corev1.Container{
		Name: "oas-trust-anchor-generator",
	}
}

func oasContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "openshift-apiserver",
	}
}

func oasSocks5ProxyContainer() *corev1.Container {
	return &corev1.Container{
		Name: "socks5-proxy",
	}
}

func buildOASTrustAnchorGenerator(oasImage string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = oasImage
		c.Command = []string{
			"/bin/bash",
			"-c",
			"cp -f /etc/pki/ca-trust/extracted/pem/* /run/ca-trust-generated/ && " +
				"if ! [[ -f /run/service-ca-signer/service-ca.crt ]]; then exit 0; fi && " +
				"chmod 0666 /run/ca-trust-generated/tls-ca-bundle.pem && " +
				"echo '#service signer ca' >> /run/ca-trust-generated/tls-ca-bundle.pem && " +
				"cat /run/service-ca-signer/service-ca.crt >>/run/ca-trust-generated/tls-ca-bundle.pem && " +
				"chmod 0444 /run/ca-trust-generated/tls-ca-bundle.pem",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildOASSocks5ProxyContainer(socks5ProxyImage string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = socks5ProxyImage
		c.Command = []string{"/usr/bin/control-plane-operator", "konnectivity-socks5-proxy"}
		c.Args = []string{"run"}
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		}
		c.Env = []corev1.EnvVar{{
			Name:  "KUBECONFIG",
			Value: "/etc/kubernetes/secrets/kubeconfig/kubeconfig",
		}}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildOASContainerMain(image string, etcdHostname string, port int32) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		cpath := func(volume, file string) string {
			return path.Join(volumeMounts.Path(c.Name, volume), file)
		}
		c.Image = image
		c.Args = []string{
			"start",
			fmt.Sprintf("--config=%s", cpath(oasVolumeConfig().Name, openshiftAPIServerConfigKey)),
			fmt.Sprintf("--authorization-kubeconfig=%s", cpath(oasVolumeKubeconfig().Name, kas.KubeconfigKey)),
			fmt.Sprintf("--authentication-kubeconfig=%s", cpath(oasVolumeKubeconfig().Name, kas.KubeconfigKey)),
			fmt.Sprintf("--requestheader-client-ca-file=%s", cpath(common.VolumeAggregatorCA().Name, certs.CASignerCertMapKey)),
			"--requestheader-allowed-names=kube-apiserver-proxy,system:kube-apiserver-proxy,system:openshift-aggregator",
			"--requestheader-username-headers=X-Remote-User",
			"--requestheader-group-headers=X-Remote-Group",
			"--requestheader-extra-headers-prefix=X-Remote-Extra-",
			fmt.Sprintf("--client-ca-file=%s", cpath(common.VolumeTotalClientCA().Name, certs.CASignerCertMapKey)),
		}
		// this list can be gathered from firewall docs: https://docs.openshift.com/container-platform/4.12/installing/install_config/configuring-firewall.html
		defaultSampleImportContainerRegistries := "quay.io,cdn03.quay.io,cdn02.quay.io,cdn01.quay.io,cdn.quay.io,registry.redhat.io,registry.access.redhat.com,access.redhat.com,sso.redhat.com"
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
				Name:  "NO_PROXY",
				Value: fmt.Sprintf("%s,%s,%s", manifests.KubeAPIServerService("").Name, etcdHostname, defaultSampleImportContainerRegistries),
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.WorkingDir = volumeMounts.Path(oasContainerMain().Name, oasVolumeWorkLogs().Name)
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "https",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}
}

func oasVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "work-logs",
	}
}

func buildOASVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func oasVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildOASVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.OpenShiftAPIServerConfig("").Name
}

func oasVolumeAuditConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "audit-config",
	}
}

func buildOASVolumeAuditConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.OpenShiftAPIServerAuditConfig("").Name
}

func oasAuditWebhookConfigFileVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "oas-audit-webhook",
	}
}

func buildOASAuditWebhookConfigFileVolume(auditWebhookRef *corev1.LocalObjectReference) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = auditWebhookRef.Name
	}
}

func applyOASAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(oasAuditWebhookConfigFileVolume(), buildOASAuditWebhookConfigFileVolume(auditWebhookRef)))
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == oasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main openshift apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		oasAuditWebhookConfigFileVolumeMount.ContainerMounts(oasContainerMain().Name)...)
}

func oasVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildOASVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
	v.Secret.DefaultMode = pointer.Int32Ptr(0640)
}

func oasVolumeEtcdClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-ca",
	}
}

func buildOASVolumeEtcdClientCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.EtcdSignerCAConfigMap("").Name
}

func oasVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOASVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftAPIServerCertSecret("").Name
	v.Secret.DefaultMode = pointer.Int32Ptr(0640)
}

func oasVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-cert",
	}
}

func buildOASVolumeEtcdClientCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.EtcdClientSecret("").Name
	v.Secret.DefaultMode = pointer.Int32Ptr(0640)
}

func oasVolumeKonnectivityProxyCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "oas-konnectivity-proxy-cert",
	}
}

func oasVolumeKonnectivityProxyCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "oas-konnectivity-proxy-ca",
	}
}

func oasTrustAnchorVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "oas-trust-anchor",
	}
}

func serviceCASignerVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "kube-controller-manager",
	}
}

func pullSecretVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "pull-secret",
	}
}

func buildOASVolumeKonnectivityProxyCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KonnectivityClientSecret("").Name
	v.Secret.DefaultMode = pointer.Int32Ptr(0640)
}

func buildOASVolumeKonnectivityProxyCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KonnectivityCAConfigMap("").Name
}
