package vpn

import (
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		vpnContainerServer().Name: util.ContainerVolumeMounts{
			vpnVolumeServerCerts().Name:  "/etc/openvpn/server",
			vpnVolumeServerConfig().Name: "/etc/openvpn/config",
			vpnVolumeClientConfig().Name: "/etc/openvpn/client",
		},
	}
	vpnServerLabels = map[string]string{
		"app": "openvpn-server",
	}
)

func ReconcileServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: vpnServerLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: vpnServerLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				ServiceAccountName:           manifests.VPNServiceAccount(deployment.Namespace).Name,
				Containers: []corev1.Container{
					util.BuildContainer(vpnContainerServer(), buildVPNContainerServer(image)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(vpnVolumeServerCerts(), buildVPNVolumeServerCerts),
					util.BuildVolume(vpnVolumeServerConfig(), buildVPNVolumeServerConfig),
					util.BuildVolume(vpnVolumeClientConfig(), buildVPNVolumeClientConfig),
				},
			},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func vpnContainerServer() *corev1.Container {
	return &corev1.Container{
		Name: "vpn-server",
	}
}

func buildVPNContainerServer(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/usr/sbin/openvpn",
		}
		c.Args = []string{
			"--config",
			path.Join(volumeMounts.Path(c.Name, vpnVolumeServerConfig().Name), vpnServerConfigKey),
		}
		c.WorkingDir = volumeMounts.Path(c.Name, vpnVolumeServerCerts().Name)
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func vpnVolumeServerCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "certs",
	}
}

func buildVPNVolumeServerCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.VPNServerCertSecret("").Name,
	}
}

func vpnVolumeServerConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildVPNVolumeServerConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.VPNServerConfig("").Name,
		},
	}
}

func vpnVolumeClientConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "client",
	}
}

func buildVPNVolumeClientConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.VPNServerClientConfig("").Name,
		},
	}
}
