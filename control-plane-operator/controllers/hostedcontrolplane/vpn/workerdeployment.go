package vpn

import (
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

const (
	libModulesPath = "/lib/modules"
)

var (
	workerVolumeMounts = util.PodVolumeMounts{
		vpnContainerClient().Name: util.ContainerVolumeMounts{
			vpnVolumeWorkerClientConfig().Name: "/etc/openvpn/config",
			vpnVolumeClientCerts().Name:        "/etc/openvpn/client",
			vpnVolumeClientHostModules().Name:  libModulesPath,
		},
	}
	vpnClientLabels = map[string]string{
		"app": "openvpn-client",
	}
)

func ReconcileWorkerClientDeployment(cm *corev1.ConfigMap, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, vpnImage string) error {
	ownerRef.ApplyTo(cm)
	clientDeployment := manifests.VPNClientDeployment()
	if err := reconcileClientDeployment(clientDeployment, deploymentConfig, vpnImage); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, clientDeployment)
}

func reconcileClientDeployment(deployment *appsv1.Deployment, deploymentConfig config.DeploymentConfig, vpnImage string) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: vpnClientLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: vpnClientLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				Containers: []corev1.Container{
					util.BuildContainer(vpnContainerClient(), buildVPNContainerClient(vpnImage)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(vpnVolumeWorkerClientConfig(), buildVPNVolumeWorkerClientConfig),
					util.BuildVolume(vpnVolumeClientCerts(), buildVPNVolumeClientCerts),
					util.BuildVolume(vpnVolumeClientHostModules(), buildVPNVolumeClientHostModules),
				},
			},
		},
	}

	deploymentConfig.ApplyTo(deployment)
	return nil
}

// TODO: Parameterize VPN CIDR
const clientScript = `
#!/bin/bash
set -eu
iptables -t nat -A POSTROUTING -s 192.168.255.0/24 -j MASQUERADE
exec /usr/sbin/openvpn --config %s
`

func vpnClientScript(configPath string) string {
	return fmt.Sprintf(clientScript, path.Join(configPath, clientConfigKey))
}

func vpnContainerClient() *corev1.Container {
	return &corev1.Container{
		Name: "openvpn-client",
	}
}

func buildVPNContainerClient(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/bin/bash",
		}
		c.Args = []string{
			"-c",
			vpnClientScript(workerVolumeMounts.Path(c.Name, vpnVolumeWorkerClientConfig().Name)),
		}
		c.WorkingDir = workerVolumeMounts.Path(c.Name, vpnVolumeClientCerts().Name)
		c.VolumeMounts = workerVolumeMounts.ContainerMounts(c.Name)
	}
}

func vpnVolumeClientCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "certs",
	}
}

func buildVPNVolumeClientCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.VPNClientSecret().Name,
	}
}

func vpnVolumeWorkerClientConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildVPNVolumeWorkerClientConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.VPNClientConfig().Name,
		},
	}
}

func vpnVolumeClientHostModules() *corev1.Volume {
	return &corev1.Volume{
		Name: "host-modules",
	}
}

func buildVPNVolumeClientHostModules(v *corev1.Volume) {
	v.HostPath = &corev1.HostPathVolumeSource{
		Path: libModulesPath,
	}
}
