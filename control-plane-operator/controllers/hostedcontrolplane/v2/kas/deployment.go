package kas

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
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
	kasNamedCertificateMountPathPrefix = "/etc/kubernetes/certs/named"

	awsPodIdentityWebhookServingCertVolumeName = "aws-pod-identity-webhook-serving-certs"
	awsPodIdentityWebhookKubeconfigVolumeName  = "aws-pod-identity-webhook-kubeconfig"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	updateMainContainer(&deployment.Spec.Template.Spec, hcp)

	util.UpdateContainer("konnectivity-server", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		serverCount := config.DefaultReplicas(hcp, true)
		c.Args = append(c.Args,
			"--server-count",
			strconv.Itoa(serverCount),
		)

		cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile())
		if len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
	})

	payloadVersion := cpContext.UserReleaseImageProvider.Version()
	if err := updateBootstrapInitContainer(deployment, hcp, payloadVersion); err != nil {
		return err
	}

	if hcp.Spec.Configuration.GetAuditPolicyConfig().Profile == configv1.NoneAuditProfileType {
		util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
	}

	// With managed etcd, we should wait for the known etcd client service name to
	// at least resolve before starting up to avoid futile connection attempts and
	// pod crashing. For unmanaged, make no assumptions.
	if hcp.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		util.RemoveInitContainer("wait-for-etcd", &deployment.Spec.Template.Spec)
	}

	if portieris, ok := hcp.Annotations[hyperv1.PortierisImageAnnotation]; ok {
		applyPortieriesConfig(&deployment.Spec.Template.Spec, portieris)
	}

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		applyAWSPodIdentityWebhookContainer(&deployment.Spec.Template.Spec, hcp)
	}

	if hcp.Spec.AuditWebhook != nil && len(hcp.Spec.AuditWebhook.Name) > 0 {
		applyKASAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, hcp.Spec.AuditWebhook)
	}

	if secretEncryption := hcp.Spec.SecretEncryption; secretEncryption != nil {
		applyGenericSecretEncryptionConfig(&deployment.Spec.Template.Spec)
		switch secretEncryption.Type {
		case hyperv1.KMS:
			if err := applyKMSConfig(&deployment.Spec.Template.Spec, secretEncryption, newKMSImages(hcp)); err != nil {
				return err
			}
		}
	}

	return nil
}

func updateMainContainer(podSpec *corev1.PodSpec, hcp *hyperv1.HostedControlPlane) {
	util.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.Ports[0].ContainerPort = util.KASPodPort(hcp)

		kasVerbosityLevel := 2
		if hcp.Annotations[hyperv1.KubeAPIServerVerbosityLevelAnnotation] != "" {
			parsedKASVerbosityValue, err := strconv.Atoi(hcp.Annotations[hyperv1.KubeAPIServerVerbosityLevelAnnotation])
			if err == nil {
				kasVerbosityLevel = parsedKASVerbosityValue
			}
		}
		c.Args = append(c.Args,
			fmt.Sprintf("--v=%d", kasVerbosityLevel),
		)

		// We have to exempt the pod and service CIDR, otherwise the proxy will get respected by the transport inside
		// the the egress transport and that breaks the egress selection/konnektivity usage.
		// Using a CIDR is not supported by Go's default ProxyFunc, but Kube uses a custom one by default that does support it:
		// https://github.com/kubernetes/kubernetes/blob/ab13c85316015cf9f115e29923ba9740bd1564fd/staging/src/k8s.io/apimachinery/pkg/util/net/http.go#L112-L114
		var additionalNoProxyCIDRS []string
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, util.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork)...)
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, util.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork)...)
		proxy.SetEnvVars(&c.Env, additionalNoProxyCIDRS...)

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

		if hcp.Spec.SecretEncryption != nil {
			// Adjust KAS liveness probe to not have a hard dependency on kms so problems isolated to kms don't
			// cause the entire kube-apiserver to restart and potentially enter CrashloopBackoff
			totalProviderInstances := 0
			switch hcp.Spec.SecretEncryption.Type {
			case hyperv1.KMS:
				if hcp.Spec.SecretEncryption.KMS != nil {
					switch hcp.Spec.SecretEncryption.KMS.Provider {
					case hyperv1.AWS:
						if hcp.Spec.SecretEncryption.KMS.AWS != nil {
							// Always will have an active key
							totalProviderInstances = 1
							if hcp.Spec.SecretEncryption.KMS.AWS.BackupKey != nil && len(hcp.Spec.SecretEncryption.KMS.AWS.BackupKey.ARN) > 0 {
								totalProviderInstances++
							}
						}
					}
				}
				// TODO: also adjust LivenessProbe for azure/ibm kms?
			}
			for i := 0; i < totalProviderInstances; i++ {
				c.LivenessProbe.HTTPGet.Path = c.LivenessProbe.HTTPGet.Path + fmt.Sprintf("&exclude=kms-provider-%d", i)
			}
		}

		for i, namedCert := range hcp.Spec.Configuration.GetNamedCertificates() {
			volumeName := fmt.Sprintf("named-cert-%d", i+1)
			podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
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
				MountPath: fmt.Sprintf("%s-%d", kasNamedCertificateMountPathPrefix, i+1),
			})
		}
	})
}

func applyKASAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, buildKASAuditWebhookConfigFileVolume(auditWebhookRef))

	util.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, kasAuditWebhookConfigFileVolumeMount.ContainerMounts(ComponentName)...)
	})
}

func applyGenericSecretEncryptionConfig(podSpec *corev1.PodSpec) {
	podSpec.Volumes = append(podSpec.Volumes, buildVolumeSecretEncryptionConfigFile())

	util.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--encryption-provider-config=%s/%s", genericSecretEncryptionConfigFileVolumeMount.Path(ComponentName, secretEncryptionConfigFileVolumeName), secretEncryptionConfigurationKey))

		c.VolumeMounts = append(c.VolumeMounts, genericSecretEncryptionConfigFileVolumeMount.ContainerMounts(ComponentName)...)
	})
}

func updateBootstrapInitContainer(deployment *appsv1.Deployment, hcp *hyperv1.HostedControlPlane, payloadVersion string) error {
	clusterFeatureGate := configv1.FeatureGate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: configv1.SchemeGroupVersion.String(),
			Kind:       "FeatureGate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.FeatureGate != nil {
		clusterFeatureGate.Spec = *hcp.Spec.Configuration.FeatureGate
	}
	featureGateBuffer := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(&clusterFeatureGate, featureGateBuffer); err != nil {
		return fmt.Errorf("failed to encode feature gates: %w", err)
	}
	featureGateYaml := featureGateBuffer.String()

	util.UpdateContainer("init-bootstrap", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "PAYLOAD_VERSION",
				Value: payloadVersion,
			},
			corev1.EnvVar{
				Name:  "FEATURE_GATE_YAML",
				Value: featureGateYaml,
			},
		)
	})

	return nil
}

func applyAWSPodIdentityWebhookContainer(podSpec *corev1.PodSpec, hcp *hyperv1.HostedControlPlane) {
	podSpec.Containers = append(podSpec.Containers, corev1.Container{
		Name:            "aws-pod-identity-webhook",
		Image:           "aws-pod-identity-webhook",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command: []string{
			"/usr/bin/aws-pod-identity-webhook",
			"--annotation-prefix=eks.amazonaws.com",
			"--in-cluster=false",
			"--kubeconfig=/var/run/app/kubeconfig/kubeconfig",
			"--logtostderr",
			"--port=4443",
			fmt.Sprintf("--aws-default-region=%s", hcp.Spec.Platform.AWS.Region),
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

	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{
			Name: awsPodIdentityWebhookServingCertVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: manifests.AWSPodIdentityWebhookServingCert("").Name},
			}},
		corev1.Volume{
			Name: awsPodIdentityWebhookKubeconfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: manifests.AWSPodIdentityWebhookKubeconfig("").Name},
			}},
	)
}

func buildKASAuditWebhookConfigFileVolume(auditWebhookRef *corev1.LocalObjectReference) corev1.Volume {
	v := corev1.Volume{
		Name: auditWebhookConfigFileVolumeName,
	}
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = auditWebhookRef.Name
	return v
}

const (
	workLogsVolumeName             = "logs"
	authConfigVolumeName           = "auth-config"
	auditConfigVolumeName          = "audit-config"
	serverCertVolumeName           = "server-crt"
	serverPrivateCertVolumeName    = "server-private-crt"
	kubeletClientCAVolumeName      = "kubelet-client-ca"
	aggregatorCertVolumeName       = "aggregator-crt"
	egressSelectorConfigVolumeName = "egress-selector-config"
	serviceAccountKeyVolumeName    = "svcacct-key"
	kubeletClientCertVolumeName    = "kubelet-client-crt"
	etcdClientCertVolumeName       = "etcd-client-crt"
	oauthMetadataVolumeName        = "oauth-metadata"
	etcdCAVolumeName               = "etcd-ca"

	authTokenWebhookConfigVolumeName = "auth-token-webhook-config"
	cloudConfigVolumeName            = "cloud-config"

	auditWebhookConfigFileVolumeName = "kas-audit-webhook"
)

var (
	volumeMounts = util.PodVolumeMounts{
		ComponentName: {
			workLogsVolumeName:                "/var/log/kube-apiserver",
			authConfigVolumeName:              "/etc/kubernetes/auth",
			auditConfigVolumeName:             "/etc/kubernetes/audit",
			serverCertVolumeName:              "/etc/kubernetes/certs/server",
			serverPrivateCertVolumeName:       "/etc/kubernetes/certs/server-private",
			aggregatorCertVolumeName:          "/etc/kubernetes/certs/aggregator",
			etcdCAVolumeName:                  "/etc/kubernetes/certs/etcd-ca",
			etcdClientCertVolumeName:          "/etc/kubernetes/certs/etcd",
			serviceAccountKeyVolumeName:       "/etc/kubernetes/secrets/svcacct-key",
			oauthMetadataVolumeName:           "/etc/kubernetes/oauth",
			authTokenWebhookConfigVolumeName:  "/etc/kubernetes/auth-token-webhook",
			kubeletClientCertVolumeName:       "/etc/kubernetes/certs/kubelet",
			kubeletClientCAVolumeName:         "/etc/kubernetes/certs/kubelet-ca",
			egressSelectorConfigVolumeName:    "/etc/kubernetes/egress-selector",
			common.VolumeAggregatorCA().Name:  "/etc/kubernetes/certs/aggregator-ca",
			common.VolumeTotalClientCA().Name: "/etc/kubernetes/certs/client-ca",
		},
	}

	cloudProviderConfigVolumeMount = util.PodVolumeMounts{
		ComponentName: {
			cloudConfigVolumeName: "/etc/kubernetes/cloud",
		},
	}

	kasAuditWebhookConfigFileVolumeMount = util.PodVolumeMounts{
		ComponentName: {
			auditWebhookConfigFileVolumeName: "/etc/kubernetes/auditwebhook",
		},
	}

	genericSecretEncryptionConfigFileVolumeMount = util.PodVolumeMounts{
		ComponentName: {
			secretEncryptionConfigFileVolumeName: "/etc/kubernetes/secret-encryption",
		},
	}
)
