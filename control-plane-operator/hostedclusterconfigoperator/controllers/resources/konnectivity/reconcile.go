package konnectivity

import (
	"fmt"
	"path"

	"k8s.io/utils/pointer"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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
		},
	}
)

func konnectivityAgentLabels() map[string]string {
	return map[string]string{
		"app":                         "konnectivity-agent",
		hyperv1.ControlPlaneComponent: "konnectivity-agent",
	}
}

func ReconcileAgentDaemonSet(daemonset *appsv1.DaemonSet, deploymentConfig config.DeploymentConfig, image string, host string, port int32, platform hyperv1.PlatformType) {
	daemonset.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityAgentLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityAgentLabels(),
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsUser: pointer.Int64Ptr(1000),
				},
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityWorkerAgentContainer(image, host, port)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeWorkerAgentCerts),
				},
			},
		},
	}
	if platform != hyperv1.IBMCloudPlatform {
		daemonset.Spec.Template.Spec.HostNetwork = true
		// Default is not the default, it means that the kubelets will re-use the hosts DNS resolver
		daemonset.Spec.Template.Spec.DNSPolicy = corev1.DNSDefault
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

func buildKonnectivityWorkerAgentContainer(image, host string, port int32) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityAgentContainer().Name, volume), file)
	}
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/usr/bin/proxy-agent",
		}
		c.Args = []string{
			"--logtostderr=true",
			"--ca-cert",
			cpath(konnectivityVolumeAgentCerts().Name, "ca.crt"),
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
			"30s",
			"--sync-interval",
			"1m",
			"--sync-interval-cap",
			"5m",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildKonnectivityVolumeWorkerAgentCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityAgentSecret("").Name,
	}
}

func ReconcileKonnectivityAgentSecret(secret, source *corev1.Secret) {
	secret.Data = map[string][]byte{}
	for k, v := range source.Data {
		secret.Data[k] = v
	}
}
