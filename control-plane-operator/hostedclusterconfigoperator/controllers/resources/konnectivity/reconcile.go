package konnectivity

import (
	"fmt"
	"path"

	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	healthPort = 2041
)

var (
	volumeMounts = util.PodVolumeMounts{
		konnectivityAgentContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeAgentCerts().Name: "/etc/konnectivity/agent",
			konnectivityVolumeCACert().Name:     "/etc/konnectivity/ca",
		},
	}
	maxUnavailable = intstr.FromString("10%")
	maxSurge       = intstr.FromInt32(0)
)

func ReconcileAgentDaemonSet(daemonset *appsv1.DaemonSet, deploymentConfig config.DeploymentConfig, image string, host string, port int32, platform hyperv1.PlatformSpec, proxy configv1.ProxyStatus) {
	var labels map[string]string
	if daemonset.Spec.Selector != nil && daemonset.Spec.Selector.MatchLabels != nil {
		labels = daemonset.Spec.Selector.MatchLabels
	} else {
		labels = map[string]string{
			"app": "konnectivity-agent",
		}
	}

	daemonset.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
			},
			Spec: corev1.PodSpec{
				// Default is not the default, it means that the kubelets will reuse the hosts DNS resolver
				DNSPolicy:                    corev1.DNSDefault,
				HostNetwork:                  true,
				AutomountServiceAccountToken: ptr.To(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsUser: ptr.To[int64](1000),
				},
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityWorkerAgentContainer(image, host, port, proxy)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeWorkerAgentCerts),
					util.BuildVolume(konnectivityVolumeCACert(), buildKonnectivityVolumeCACert),
				},
			},
		},
		UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
			Type: appsv1.RollingUpdateDaemonSetStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDaemonSet{
				MaxUnavailable: &maxUnavailable,
				MaxSurge:       &maxSurge,
			},
		},
	}
	// IBMCloud requires the following settings
	if platform.Type == hyperv1.IBMCloudPlatform {
		daemonset.Spec.Template.Spec.HostNetwork = false
		if platform.IBMCloud != nil && platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
			daemonset.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst
		}
	}
	deploymentConfig.ApplyToDaemonSet(daemonset)
}

func konnectivityAgentContainer() *corev1.Container {
	return &corev1.Container{
		Name: "konnectivity-agent",
	}
}

func konnectivityVolumeAgentCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "agent-certs",
	}
}

func konnectivityVolumeCACert() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-ca",
	}
}

func buildKonnectivityWorkerAgentContainer(image, host string, port int32, proxy configv1.ProxyStatus) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityAgentContainer().Name, volume), file)
	}
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{
			"/usr/bin/proxy-agent",
		}
		c.Args = []string{
			"--logtostderr=true",
			"--ca-cert",
			cpath(konnectivityVolumeCACert().Name, "ca.crt"),
			"--agent-cert",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSCertKey),
			"--agent-key",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSPrivateKeyKey),
			"--proxy-server-host",
			host,
			"--proxy-server-port",
			fmt.Sprint(port),
			"--health-server-port",
			fmt.Sprint(healthPort),
			"--agent-identifiers=default-route=true",
			"--keepalive-time",
			"30s",
			"--probe-interval",
			"5s",
			"--sync-interval",
			"5s",
			"--sync-interval-cap",
			"30s",
			"--v",
			"3",
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "HTTP_PROXY",
				Value: proxy.HTTPProxy,
			},
			{
				Name:  "HTTPS_PROXY",
				Value: proxy.HTTPSProxy,
			},
			{
				Name:  "NO_PROXY",
				Value: proxy.NoProxy,
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildKonnectivityVolumeWorkerAgentCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KonnectivityAgentSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func buildKonnectivityVolumeCACert(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		DefaultMode: ptr.To[int32](0640),
	}
	v.ConfigMap.Name = manifests.KonnectivityCAConfigMap("").Name
}

func ReconcileKonnectivityAgentSecret(secret, source *corev1.Secret) {
	secret.Data = map[string][]byte{}
	for k, v := range source.Data {
		secret.Data[k] = v
	}
}
