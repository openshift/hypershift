package kas

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	kasNamedCertificateMountPathPrefix         = "/etc/kubernetes/certs/named"
	authConfigHashAnnotation                   = "kube-apiserver.hypershift.openshift.io/auth-config-hash"
	auditConfigHashAnnotation                  = "kube-apiserver.hypershift.openshift.io/audit-config-hash"
	configHashAnnotation                       = "kube-apiserver.hypershift.openshift.io/config-hash"
	awsPodIdentityWebhookServingCertVolumeName = "aws-pod-identity-webhook-serving-certs"
	awsPodIdentityWebhookKubeconfigVolumeName  = "aws-pod-identity-webhook-kubeconfig"
)

var (
	volumeMounts = util.PodVolumeMounts{
		kasContainerBootstrap().Name: {
			kasVolumeBootstrapManifests().Name:  "/work",
			kasVolumeLocalhostKubeconfig().Name: "/var/secrets/localhost-kubeconfig",
		},
		kasContainerBootstrapRender().Name: {
			kasVolumeBootstrapManifests().Name: "/work",
		},
		kasContainerMain().Name: {
			kasVolumeWorkLogs().Name:               "/var/log/kube-apiserver",
			kasVolumeAuthConfig().Name:             "/etc/kubernetes/auth",
			kasVolumeConfig().Name:                 "/etc/kubernetes/config",
			kasVolumeAuditConfig().Name:            "/etc/kubernetes/audit",
			kasVolumeKonnectivityCA().Name:         "/etc/kubernetes/certs/konnectivity-ca",
			kasVolumeServerCert().Name:             "/etc/kubernetes/certs/server",
			kasVolumeServerPrivateCert().Name:      "/etc/kubernetes/certs/server-private",
			kasVolumeAggregatorCert().Name:         "/etc/kubernetes/certs/aggregator",
			common.VolumeAggregatorCA().Name:       "/etc/kubernetes/certs/aggregator-ca",
			common.VolumeTotalClientCA().Name:      "/etc/kubernetes/certs/client-ca",
			kasVolumeEtcdCA().Name:                 "/etc/kubernetes/certs/etcd-ca",
			kasVolumeEtcdClientCert().Name:         "/etc/kubernetes/certs/etcd",
			kasVolumeServiceAccountKey().Name:      "/etc/kubernetes/secrets/svcacct-key",
			kasVolumeOauthMetadata().Name:          "/etc/kubernetes/oauth",
			kasVolumeAuthTokenWebhookConfig().Name: "/etc/kubernetes/auth-token-webhook",
			kasVolumeKubeletClientCert().Name:      "/etc/kubernetes/certs/kubelet",
			kasVolumeKubeletClientCA().Name:        "/etc/kubernetes/certs/kubelet-ca",
			kasVolumeKonnectivityClientCert().Name: "/etc/kubernetes/certs/konnectivity-client",
			kasVolumeEgressSelectorConfig().Name:   "/etc/kubernetes/egress-selector",
		},
		konnectivityServerContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeServerCerts().Name:  "/etc/konnectivity/server",
			konnectivityVolumeClusterCerts().Name: "/etc/konnectivity/cluster",
			kasVolumeKonnectivityCA().Name:        "/etc/konnectivity/ca",
		},
	}

	cloudProviderConfigVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeCloudConfig().Name: "/etc/kubernetes/cloud",
		},
	}

	kasAuditWebhookConfigFileVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasAuditWebhookConfigFileVolume().Name: "/etc/kubernetes/auditwebhook",
		},
	}

	genericSecretEncryptionConfigFileVolumeMount = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeSecretEncryptionConfigFile().Name: "/etc/kubernetes/secret-encryption",
		},
	}
)

func kasLabels() map[string]string {
	return map[string]string{
		"app":                              "kube-apiserver",
		hyperv1.ControlPlaneComponentLabel: "kube-apiserver",
	}
}

func ReconcileKubeAPIServerDeployment(deployment *appsv1.Deployment,
	hcp *hyperv1.HostedControlPlane,
	ownerRef config.OwnerRef,
	deploymentConfig config.DeploymentConfig,
	namedCertificates []configv1.APIServerNamedServingCert,
	cloudProviderName string,
	cloudProviderConfigRef *corev1.LocalObjectReference,
	cloudProviderCreds *corev1.LocalObjectReference,
	images KubeAPIServerImages,
	config *corev1.ConfigMap,
	auditConfig *corev1.ConfigMap,
	authConfig *corev1.ConfigMap,
	auditWebhookRef *corev1.LocalObjectReference,
	aesCBCActiveKey []byte,
	aesCBCBackupKey []byte,
	port int32,
	payloadVersion string,
	featureGateSpec *configv1.FeatureGateSpec,
	oidcCA *corev1.LocalObjectReference,
	cipherSuites []string,
) error {

	secretEncryptionData := hcp.Spec.SecretEncryption
	etcdMgmtType := hcp.Spec.Etcd.ManagementType
	var additionalNoProxyCIDRS []string
	additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, util.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork)...)
	additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, util.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork)...)

	configBytes, ok := config.Data[KubeAPIServerConfigKey]
	if !ok {
		return fmt.Errorf("kube apiserver configuration is not expected to be empty")
	}
	configHash := util.ComputeHash(configBytes)

	auditConfigBytes, ok := auditConfig.Data[AuditPolicyConfigMapKey]
	if !ok {
		return fmt.Errorf("kube apiserver audit configuration is not expected to be empty")
	}
	auditConfigHash := util.ComputeHash(auditConfigBytes)

	authConfigBytes, ok := authConfig.Data[AuthConfigMapKey]
	if !ok {
		return fmt.Errorf("kube apiserver authentication configuration is not expected to be empty")
	}
	authConfigHash := util.ComputeHash(authConfigBytes)

	// preserve existing resource requirements for main KAS container
	kasContainer := util.FindContainer(kasContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if kasContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(kasContainer)
	}
	// preserve existing resource requirements for the konnectivy-server container
	konnectivityContainer := util.FindContainer(konnectivityServerContainer().Name, deployment.Spec.Template.Spec.Containers)
	if konnectivityContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(konnectivityContainer)
	}

	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: kasLabels(),
		}
	}

	clusterFeatureGate := configv1.FeatureGate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "FeatureGate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if featureGateSpec != nil {
		clusterFeatureGate.Spec = *featureGateSpec
	}
	featureGateBuffer := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(&clusterFeatureGate, featureGateBuffer); err != nil {
		return fmt.Errorf("failed to encode feature gates: %w", err)
	}
	featureGateYaml := featureGateBuffer.String()

	deployment.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: kasLabels(),
			Annotations: map[string]string{
				configHashAnnotation:      configHash,
				auditConfigHashAnnotation: auditConfigHash,
				authConfigHashAnnotation:  authConfigHash,
			},
		},
		Spec: corev1.PodSpec{
			DNSPolicy:       corev1.DNSClusterFirst,
			RestartPolicy:   corev1.RestartPolicyAlways,
			SecurityContext: &corev1.PodSecurityContext{},
			// The KAS takes 130 seconds to finish its graceful shutdown, give it enough
			// time to do that + 5 seconds margin. The shutdown sequence is described
			// in detail here: https://github.com/openshift/installer/blob/master/docs/dev/kube-apiserver-health-check.md
			TerminationGracePeriodSeconds: ptr.To[int64](135),
			SchedulerName:                 corev1.DefaultSchedulerName,
			AutomountServiceAccountToken:  ptr.To(false),
			InitContainers: []corev1.Container{
				util.BuildContainer(kasContainerBootstrapRender(), buildKASContainerBootstrapRender(images.ClusterConfigOperator, payloadVersion, featureGateYaml)),
			},
			Containers: []corev1.Container{
				util.BuildContainer(kasContainerBootstrap(), buildKASContainerNewBootstrap(images.KASBootstrap)),
				util.BuildContainer(kasContainerMain(), buildKASContainerMain(images.HyperKube, port, additionalNoProxyCIDRS, hcp)),
				util.BuildContainer(konnectivityServerContainer(), buildKonnectivityServerContainer(images.KonnectivityServer, deploymentConfig.Replicas, cipherSuites)),
			},
			Volumes: []corev1.Volume{
				util.BuildVolume(kasVolumeBootstrapManifests(), buildKASVolumeBootstrapManifests),
				util.BuildVolume(kasVolumeLocalhostKubeconfig(), buildKASVolumeLocalhostKubeconfig),
				util.BuildVolume(kasVolumeWorkLogs(), buildKASVolumeWorkLogs),
				util.BuildVolume(kasVolumeConfig(), buildKASVolumeConfig),
				util.BuildVolume(kasVolumeAuthConfig(), buildKASVolumeAuthConfig),
				util.BuildVolume(kasVolumeAuditConfig(), buildKASVolumeAuditConfig),
				util.BuildVolume(kasVolumeKonnectivityCA(), buildKASVolumeKonnectivityCA),
				util.BuildVolume(kasVolumeServerCert(), buildKASVolumeServerCert),
				util.BuildVolume(kasVolumeServerPrivateCert(), buildKASVolumeServerPrivateCert),
				util.BuildVolume(kasVolumeAggregatorCert(), buildKASVolumeAggregatorCert),
				util.BuildVolume(common.VolumeAggregatorCA(), common.BuildVolumeAggregatorCA),
				util.BuildVolume(kasVolumeServiceAccountKey(), buildKASVolumeServiceAccountKey),
				util.BuildVolume(kasVolumeEtcdCA(), buildKASVolumeEtcdCA),
				util.BuildVolume(kasVolumeEtcdClientCert(), buildKASVolumeEtcdClientCert),
				util.BuildVolume(kasVolumeOauthMetadata(), buildKASVolumeOauthMetadata),
				util.BuildVolume(kasVolumeAuthTokenWebhookConfig(), buildKASVolumeAuthTokenWebhookConfig),
				util.BuildVolume(common.VolumeTotalClientCA(), common.BuildVolumeTotalClientCA),
				util.BuildVolume(kasVolumeKubeletClientCert(), buildKASVolumeKubeletClientCert),
				util.BuildVolume(kasVolumeKubeletClientCA(), buildKASVolumeKubeletClientCA),
				util.BuildVolume(kasVolumeKonnectivityClientCert(), buildKASVolumeKonnectivityClientCert),
				util.BuildVolume(kasVolumeEgressSelectorConfig(), buildKASVolumeEgressSelectorConfig),
				util.BuildVolume(kasVolumeKubeconfig(), buildKASVolumeKubeconfig),
				util.BuildVolume(konnectivityVolumeServerCerts(), buildKonnectivityVolumeServerCerts),
				util.BuildVolume(konnectivityVolumeClusterCerts(), buildKonnectivityVolumeClusterCerts),
			},
		},
	}

	if auditConfig.Data[AuditPolicyProfileMapKey] != string(configv1.NoneAuditProfileType) {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:                     "audit-logs",
			Image:                    images.CLI,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			Command:                  []string{"/bin/bash"},
			Args: []string{
				"-c",
				RenderAuditLogScript(fmt.Sprintf("%s/%s", volumeMounts.Path(kasContainerMain().Name, kasVolumeWorkLogs().Name), "audit.log")),
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("5m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      kasVolumeWorkLogs().Name,
				MountPath: volumeMounts.Path(kasContainerMain().Name, kasVolumeWorkLogs().Name),
			}},
		})
	}

	// With managed etcd, we should wait for the known etcd client service name to
	// at least resolve before starting up to avoid futile connection attempts and
	// pod crashing. For unmanaged, make no assumptions.
	if etcdMgmtType == hyperv1.Managed {
		deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers,
			util.BuildContainer(kasContainerWaitForEtcd(), buildKASContainerWaitForEtcd(images.CLI, deployment.Namespace)))
	}

	if len(images.Portieris) > 0 {
		applyPortieriesConfig(&deployment.Spec.Template.Spec, images.Portieris)
	}
	applyNamedCertificateMounts(namedCertificates, &deployment.Spec.Template.Spec)
	applyCloudConfigVolumeMount(cloudProviderConfigRef, &deployment.Spec.Template.Spec)
	util.ApplyCloudProviderCreds(&deployment.Spec.Template.Spec, cloudProviderName, cloudProviderCreds, images.TokenMinterImage, kasContainerMain().Name)

	if cloudProviderName == aws.Provider {
		deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
			Name:                     "aws-pod-identity-webhook",
			Image:                    images.AWSPodIdentityWebhookImage,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			Command: []string{
				"/usr/bin/aws-pod-identity-webhook",
				"--annotation-prefix=eks.amazonaws.com",
				"--in-cluster=false",
				"--kubeconfig=/var/run/app/kubeconfig/kubeconfig",
				"--logtostderr",
				"--port=4443",
				"--aws-default-region=" + hcp.Spec.Platform.AWS.Region,
				"--tls-cert=/var/run/app/certs/tls.crt",
				"--tls-key=/var/run/app/certs/tls.key",
				"--token-audience=openshift",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: awsPodIdentityWebhookServingCertVolumeName, MountPath: "/var/run/app/certs"},
				{Name: awsPodIdentityWebhookKubeconfigVolumeName, MountPath: "/var/run/app/kubeconfig"},
			},
		})
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{Name: awsPodIdentityWebhookServingCertVolumeName, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.AWSPodIdentityWebhookServingCert("").Name}}},
			corev1.Volume{Name: awsPodIdentityWebhookKubeconfigVolumeName, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.AWSPodIdentityWebhookKubeconfig("").Name}}},
		)
	}

	if auditWebhookRef != nil {
		applyKASAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, auditWebhookRef)
	}

	if secretEncryptionData != nil {
		applyGenericSecretEncryptionConfig(&deployment.Spec.Template.Spec)
		switch secretEncryptionData.Type {
		case hyperv1.KMS:
			if err := applyKMSConfig(&deployment.Spec.Template.Spec, secretEncryptionData, images); err != nil {
				return err
			}
		case hyperv1.AESCBC:
			err := applyAESCBCKeyHashAnnotation(&deployment.Spec.Template, aesCBCActiveKey, aesCBCBackupKey)
			if err != nil {
				return err
			}
		default:
			// nothing needed to be done
		}
	}
	ownerRef.ApplyTo(deployment)
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func kasContainerBootstrap() *corev1.Container {
	return &corev1.Container{
		Name: "bootstrap",
	}
}
func buildKASContainerNewBootstrap(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/usr/bin/control-plane-operator",
			"kas-bootstrap",
			"--resources-path", volumeMounts.Path(c.Name, kasVolumeBootstrapManifests().Name),
		}
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "KUBECONFIG",
				Value: path.Join(volumeMounts.Path(kasContainerBootstrap().Name, kasVolumeLocalhostKubeconfig().Name), KubeconfigKey),
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasContainerBootstrapRender() *corev1.Container {
	return &corev1.Container{
		Name: "bootstrap-render",
	}
}

func buildKASContainerBootstrapRender(image, payloadVersion, featureGateYaml string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Command = []string{
			"/bin/bash",
		}
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Args = []string{
			"-c",
			invokeBootstrapRenderScript(volumeMounts.Path(kasContainerBootstrapRender().Name, kasVolumeBootstrapManifests().Name), payloadVersion, featureGateYaml),
		}
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		}
		c.Image = image
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func kasContainerWaitForEtcd() *corev1.Container {
	return &corev1.Container{
		Name: "wait-for-etcd",
	}
}

func buildKASContainerWaitForEtcd(image string, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/bin/bash",
		}
		c.Args = []string{
			"-c",
			waitForEtcdScript(namespace),
		}
	}
}

func kasContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-apiserver",
	}
}

func buildKASContainerMain(image string, port int32, noProxyCIDRs []string, hcp *hyperv1.HostedControlPlane) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"hyperkube",
		}

		kasVerbosityLevel := 2
		if hcp.Annotations[hyperv1.KubeAPIServerVerbosityLevelAnnotation] != "" {
			parsedKASVerbosityValue, err := strconv.Atoi(hcp.Annotations[hyperv1.KubeAPIServerVerbosityLevelAnnotation])
			if err == nil {
				kasVerbosityLevel = parsedKASVerbosityValue
			}
		}

		c.Args = []string{
			"kube-apiserver",
			fmt.Sprintf("--openshift-config=%s", path.Join(volumeMounts.Path(c.Name, kasVolumeConfig().Name), KubeAPIServerConfigKey)),
			fmt.Sprintf("--v=%d", kasVerbosityLevel),
		}

		c.Env = []corev1.EnvVar{{
			// Needed by the apirequest count controller, it uses this as its nodeName. Without this, all its requests fail validation
			// as the nodeName is empty. Should be using the hostname, but it appears os.Hostname() doesn't work so it falls back to
			// the value of this env var.
			// * Controller instantiation: https://github.com/openshift/kubernetes/blob/1b2affc8e97007139e70badd729981279d4f5f1b/openshift-kube-apiserver/openshiftkubeapiserver/patch.go#L88
			// * NodeName detection: https://github.com/openshift/kubernetes/blob/1b2affc8e97007139e70badd729981279d4f5f1b/openshift-kube-apiserver/openshiftkubeapiserver/patch.go#L131
			Name:      "HOST_IP",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}},
		}}

		// We have to exempt the pod and service CIDR, otherwise the proxy will get respected by the transport inside
		// the the egress transport and that breaks the egress selection/konnektivity usage.
		// Using a CIDR is not supported by Go's default ProxyFunc, but Kube uses a custom one by default that does support it:
		// https://github.com/kubernetes/kubernetes/blob/ab13c85316015cf9f115e29923ba9740bd1564fd/staging/src/k8s.io/apimachinery/pkg/util/net/http.go#L112-L114
		proxy.SetEnvVars(&c.Env, noProxyCIDRs...)

		if hcp.Annotations[hyperv1.KubeAPIServerGOGCAnnotation] != "" {
			c.Env = append(c.Env, corev1.EnvVar{
				Name:  "GOGC",
				Value: hcp.Annotations[hyperv1.KubeAPIServerGOGCAnnotation],
			})
		}

		if hcp.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation] != "" {
			c.Env = append(c.Env, corev1.EnvVar{
				Name:  "GOMEMLIMIT",
				Value: hcp.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation],
			})
		}

		c.WorkingDir = volumeMounts.Path(c.Name, kasVolumeWorkLogs().Name)
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "client",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}
}

func kasVolumeBootstrapManifests() *corev1.Volume {
	return &corev1.Volume{
		Name: "bootstrap-manifests",
	}
}

func buildKASVolumeBootstrapManifests(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func kasVolumeLocalhostKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "localhost-kubeconfig",
	}
}
func buildKASVolumeLocalhostKubeconfig(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASLocalhostKubeconfigSecret("").Name
}

func kasVolumeWorkLogs() *corev1.Volume {
	return &corev1.Volume{
		Name: "logs",
	}
}
func buildKASVolumeWorkLogs(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}
func kasVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kas-config",
	}
}
func buildKASVolumeConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.KASConfig("").Name
}
func kasVolumeAuthConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "auth-config",
	}
}
func buildKASVolumeAuthConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.AuthConfig("").Name
}
func kasVolumeAuditConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "audit-config",
	}
}
func buildKASVolumeAuditConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.KASAuditConfig("").Name
}
func kasVolumeKonnectivityCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-ca",
	}
}
func buildKASVolumeKonnectivityCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		DefaultMode: ptr.To[int32](0640),
	}
	v.ConfigMap.Name = manifests.KonnectivityCAConfigMap("").Name
}
func kasVolumeServerCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-crt",
	}
}
func kasVolumeServerPrivateCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-private-crt",
	}
}
func buildKASVolumeServerCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASServerCertSecret("").Name
}

func buildKASVolumeServerPrivateCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASServerPrivateCertSecret("").Name
}

func kasVolumeKubeletClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-ca",
	}
}
func buildKASVolumeKubeletClientCA(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.TotalClientCABundle("").Name
}

func kasVolumeKonnectivityClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-client",
	}
}
func buildKASVolumeKonnectivityClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KonnectivityClientSecret("").Name
}

func kasVolumeAggregatorCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-crt",
	}
}

func buildKASVolumeAggregatorCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASAggregatorCertSecret("").Name
}

func kasVolumeEgressSelectorConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "egress-selector-config",
	}
}

func buildKASVolumeEgressSelectorConfig(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.KASEgressSelectorConfig("").Name
}

func kasVolumeServiceAccountKey() *corev1.Volume {
	return &corev1.Volume{
		Name: "svcacct-key",
	}
}
func buildKASVolumeServiceAccountKey(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.ServiceAccountSigningKeySecret("").Name
}

func kasVolumeKubeletClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-client-crt",
	}
}

func buildKASVolumeKubeletClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASKubeletClientCertSecret("").Name
}

func kasVolumeEtcdClientCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-client-crt",
	}
}
func buildKASVolumeEtcdClientCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.EtcdClientSecret("").Name
}

func kasVolumeEtcdCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "etcd-ca",
	}
}

func buildKASVolumeEtcdCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.EtcdSignerCAConfigMap("").Name
}

func kasVolumeOauthMetadata() *corev1.Volume {
	return &corev1.Volume{
		Name: "oauth-metadata",
	}
}
func buildKASVolumeOauthMetadata(v *corev1.Volume) {
	if v.ConfigMap == nil {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	}
	v.ConfigMap.DefaultMode = ptr.To[int32](420)
	v.ConfigMap.Name = manifests.KASOAuthMetadata("").Name
}

func kasVolumeAuthTokenWebhookConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "auth-token-webhook-config",
	}
}
func buildKASVolumeAuthTokenWebhookConfig(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.KASAuthenticationTokenWebhookConfigSecret("").Name
}

func kasVolumeCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func buildKASVolumeCloudConfig(configMapName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.ConfigMap = &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
			DefaultMode:          ptr.To[int32](420),
		}
	}
}

func applyCloudConfigVolumeMount(configRef *corev1.LocalObjectReference, podSpec *corev1.PodSpec) {
	if configRef != nil && configRef.Name != "" {
		podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeCloudConfig(), buildKASVolumeCloudConfig(configRef.Name)))
		var container *corev1.Container
		for i, c := range podSpec.Containers {
			if c.Name == kasContainerMain().Name {
				container = &podSpec.Containers[i]
				break
			}
		}
		if container == nil {
			panic("main kube apiserver container not found in spec")
		}
		container.VolumeMounts = append(container.VolumeMounts,
			cloudProviderConfigVolumeMount.ContainerMounts(kasContainerMain().Name)...)
	}
}

func invokeBootstrapRenderScript(workDir, payloadVersion, featureGateYaml string) string {

	var script = `#!/bin/sh
cd /tmp
mkdir input output manifests

touch /tmp/manifests/99_feature-gate.yaml
cat <<EOF >/tmp/manifests/99_feature-gate.yaml
%[3]s
EOF

touch /tmp/manifests/hcco-rolebinding.yaml
cat <<EOF >/tmp/manifests/hcco-rolebinding.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hcco-cluster-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: User
  name: system:hosted-cluster-config
EOF

/usr/bin/render \
   --asset-output-dir /tmp/output \
   --rendered-manifest-dir=/tmp/manifests \
   --cluster-profile=ibm-cloud-managed \
   --payload-version=%[2]s
cp /tmp/output/manifests/* %[1]s
cp /tmp/manifests/* %[1]s
`
	return fmt.Sprintf(script, workDir, payloadVersion, featureGateYaml)
}

func waitForEtcdScript(namespace string) string {
	var script = `#!/bin/sh
while ! nslookup etcd-client.%s.svc; do sleep 1; done
`
	return fmt.Sprintf(script, namespace)
}

func applyNamedCertificateMounts(certs []configv1.APIServerNamedServingCert, spec *corev1.PodSpec) {
	var container *corev1.Container
	for i := range spec.Containers {
		if spec.Containers[i].Name == kasContainerMain().Name {
			container = &spec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("Kube APIServer container not found")
	}
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
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: fmt.Sprintf("%s-%d", kasNamedCertificateMountPathPrefix, i+1),
		})
	}
}

func kasAuditWebhookConfigFileVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "kas-audit-webhook",
	}
}

func buildKASAuditWebhookConfigFileVolume(auditWebhookRef *corev1.LocalObjectReference) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = auditWebhookRef.Name
	}
}

func applyKASAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasAuditWebhookConfigFileVolume(), buildKASAuditWebhookConfigFileVolume(auditWebhookRef)))
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == kasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		kasAuditWebhookConfigFileVolumeMount.ContainerMounts(kasContainerMain().Name)...)
}

func applyGenericSecretEncryptionConfig(podSpec *corev1.PodSpec) {
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == kasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.Args = append(container.Args, fmt.Sprintf("--encryption-provider-config=%s/%s", genericSecretEncryptionConfigFileVolumeMount.Path(kasContainerMain().Name, kasVolumeSecretEncryptionConfigFile().Name), secretEncryptionConfigurationKey))
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeSecretEncryptionConfigFile(), buildVolumeSecretEncryptionConfigFile))
	container.VolumeMounts = append(container.VolumeMounts,
		genericSecretEncryptionConfigFileVolumeMount.ContainerMounts(kasContainerMain().Name)...)
}

func kasVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildKASVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KASLocalhostKubeconfigSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func konnectivityServerContainer() *corev1.Container {
	return &corev1.Container{
		Name: "konnectivity-server",
	}
}

func buildKonnectivityServerContainer(image string, serverCount int, cipherSuites []string) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityServerContainer().Name, volume), file)
	}
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/usr/bin/proxy-server",
		}
		c.Args = []string{
			"--logtostderr=true",
			"--log-file-max-size=0",
			"--cluster-cert",
			cpath(konnectivityVolumeClusterCerts().Name, corev1.TLSCertKey),
			"--cluster-key",
			cpath(konnectivityVolumeClusterCerts().Name, corev1.TLSPrivateKeyKey),
			"--server-cert",
			cpath(konnectivityVolumeServerCerts().Name, corev1.TLSCertKey),
			"--server-key",
			cpath(konnectivityVolumeServerCerts().Name, corev1.TLSPrivateKeyKey),
			"--server-ca-cert",
			cpath(kasVolumeKonnectivityCA().Name, certs.CASignerCertMapKey),
			"--server-port",
			strconv.Itoa(KonnectivityServerLocalPort),
			"--agent-port",
			strconv.Itoa(KonnectivityServerPort),
			"--health-port",
			strconv.Itoa(KonnectivityHealthPort),
			"--admin-port=8093",
			"--mode=http-connect",
			"--proxy-strategies=destHost,defaultRoute",
			"--keepalive-time",
			"30s",
			"--frontend-keepalive-time",
			"30s",
			"--server-count",
			strconv.Itoa(serverCount),
		}

		if len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"/bin/sh",
						"-c",
						"sleep 70",
					},
				},
			},
		}
	}
}

func konnectivityVolumeServerCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-certs",
	}
}

func buildKonnectivityVolumeServerCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KonnectivityServerSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func konnectivityVolumeClusterCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-certs",
	}
}

func buildKonnectivityVolumeClusterCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KonnectivityClusterSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func RenderAuditLogScript(auditLogFilePath string) string {
	var script = `
set -o errexit
set -o nounset
set -o pipefail

function cleanup() {
	pkill -P $$$
	wait
	exit
}
trap cleanup SIGTERM

/usr/bin/tail -c+1 -F %s &
wait $!
`
	return fmt.Sprintf(script, auditLogFilePath)
}
