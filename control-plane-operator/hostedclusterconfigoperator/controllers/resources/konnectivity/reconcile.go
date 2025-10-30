package konnectivity

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
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
	maxSurge       = intstr.FromInt(0)
)

func ReconcileAgentDaemonSet(daemonset *appsv1.DaemonSet, params *KonnectivityParams, platform hyperv1.PlatformSpec, proxy configv1.ProxyStatus) {
	var labels map[string]string
	if daemonset.Spec.Selector != nil && daemonset.Spec.Selector.MatchLabels != nil {
		labels = daemonset.Spec.Selector.MatchLabels
	} else {
		labels = map[string]string{
			"app": "konnectivity-agent",
		}
	}

	annotations := make(map[string]string, len(params.AdditionalAnnotations)+1)
	for k, v := range params.AdditionalAnnotations {
		annotations[k] = v
	}
	annotations["openshift.io/required-scc"] = "restricted-v2"

	// Determine HostNetwork setting based on platform
	useHostNetwork := true
	// IBMCloud requires HostNetwork to be false
	if platform.Type == hyperv1.IBMCloudPlatform {
		useHostNetwork = false
	}

	daemonset.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				// Default is not the default, it means that the kubelets will reuse the hosts DNS resolver
				DNSPolicy:                    corev1.DNSDefault,
				HostNetwork:                  useHostNetwork,
				AutomountServiceAccountToken: ptr.To(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsUser: ptr.To[int64](1000),
				},
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityWorkerAgentContainer(params.Image, params.ExternalAddress, params.ExternalPort, proxy, useHostNetwork)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeWorkerAgentCerts),
					util.BuildVolume(konnectivityVolumeCACert(), buildKonnectivityVolumeCACert),
				},
				PriorityClassName: systemNodeCriticalPriorityClass,
				// Always run, even if nodes are not ready e.G. because there are networking issues as this helps a lot in debugging
				Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
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
	// IBMCloud UPI requires DNSClusterFirst
	if platform.Type == hyperv1.IBMCloudPlatform {
		if platform.IBMCloud != nil && platform.IBMCloud.ProviderType == configv1.IBMCloudProviderTypeUPI {
			daemonset.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst
		}
	}

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

func buildKonnectivityWorkerAgentContainer(image, host string, port int32, proxy configv1.ProxyStatus, useHostNetwork bool) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityAgentContainer().Name, volume), file)
	}

	// Determine health server host based on network mode
	// When HostNetwork is true, bind to localhost to prevent external exposure
	// When HostNetwork is false, bind to all interfaces so kubelet can reach via pod IP
	healthServerHost := "127.0.0.1"
	if !useHostNetwork {
		healthServerHost = "0.0.0.0"
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
			"--health-server-host",
			healthServerHost,
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
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.SecurityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			RunAsNonRoot:             ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("40m"),
			},
		}

		// Determine probe host based on network mode
		// When HostNetwork is true, set Host to 127.0.0.1 so kubelet contacts host's localhost
		// When HostNetwork is false, omit Host so it defaults to pod IP for kubelet to reach
		var probeHost string
		if useHostNetwork {
			probeHost = "127.0.0.1"
		}

		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Host:   probeHost,
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "healthz",
				},
			},
			TimeoutSeconds:   5,
			PeriodSeconds:    30,
			FailureThreshold: 6,
			SuccessThreshold: 1,
		}
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Host:   probeHost,
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "readyz",
				},
			},
			TimeoutSeconds:   5,
			PeriodSeconds:    30,
			FailureThreshold: 1,
			SuccessThreshold: 1,
		}
		c.StartupProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Host:   probeHost,
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(healthPort)),
					Path:   "healthz",
				},
			},
			TimeoutSeconds:   5,
			PeriodSeconds:    5,
			FailureThreshold: 60,
			SuccessThreshold: 1,
		}
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
