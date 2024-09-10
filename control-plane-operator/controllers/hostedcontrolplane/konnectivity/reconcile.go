package konnectivity

import (
	"bytes"
	"fmt"
	"path"

	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	konnectivityAgentName = "konnectivity-agent"
)

var (
	volumeMounts = util.PodVolumeMounts{
		konnectivityAgentContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeAgentCerts().Name: "/etc/konnectivity/agent",
			konnectivitySignerCA().Name:         "/etc/konnectivity/ca",
		},
	}
)

func konnectivityAgentLabels() map[string]string {
	return map[string]string{
		"app":                              konnectivityAgentName,
		hyperv1.ControlPlaneComponentLabel: konnectivityAgentName,
	}
}

const (
	KubeconfigKey = "kubeconfig"
)

func konnectivitySignerCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-ca",
	}
}

func buildKonnectivitySignerCAkonnectivitySignerCAVolume(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KonnectivityCAConfigMap("").Name
}

func konnectivityAgentContainer() *corev1.Container {
	return &corev1.Container{
		Name: konnectivityAgentName,
	}
}

func konnectivityVolumeAgentCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "agent-certs",
	}
}

func buildKonnectivityVolumeAgentCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KonnectivityAgentSecret("").Name,
		DefaultMode: ptr.To[int32](0640),
	}
}

func ReconcileAgentDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, ips []string) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main scheduler container
	mainContainer := util.FindContainer(konnectivityAgentName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: konnectivityAgentLabels(),
	}
	deployment.Spec.Template = corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: konnectivityAgentLabels(),
		},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: ptr.To(false),
			Containers: []corev1.Container{
				util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityAgentContainer(image, ips)),
			},
			Volumes: []corev1.Volume{
				util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeAgentCerts),
				util.BuildVolume(konnectivitySignerCA(), buildKonnectivitySignerCAkonnectivitySignerCAVolume),
			},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func buildKonnectivityAgentContainer(image string, ips []string) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityAgentContainer().Name, volume), file)
	}
	var agentIDs bytes.Buffer
	separator := ""
	for i, ip := range ips {
		agentIDs.WriteString(fmt.Sprintf("%sipv4=%s", separator, ip))
		if i == 0 {
			separator = "&"
		}
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
			cpath(konnectivitySignerCA().Name, certs.CASignerCertMapKey),
			"--agent-cert",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSCertKey),
			"--agent-key",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSPrivateKeyKey),
			"--proxy-server-host",
			manifests.KonnectivityServerService("").Name,
			"--proxy-server-port",
			fmt.Sprint(kas.KonnectivityServerPort),
			"--health-server-port",
			fmt.Sprint(kas.KonnectivityHealthPort),
			"--agent-identifiers",
			agentIDs.String(),
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
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}
