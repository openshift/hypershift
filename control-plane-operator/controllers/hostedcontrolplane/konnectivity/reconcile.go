package konnectivity

import (
	"fmt"
	"path"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		konnectivityServerContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeServerCA().Name:     "/etc/konnectivity/server-ca",
			konnectivityVolumeServerCerts().Name:  "/etc/konnectivity/server",
			konnectivityVolumeClusterCerts().Name: "/etc/konnectivity/cluster",
			konnectivityVolumeKubeconfig().Name:   "/etc/konnectivity/kubeconfig",
		},
	}
	konnectivityServerLabels = map[string]string{
		"app": "konnectivity-server",
	}
)

const (
	KubeconfigKey = "kubeconfig"
)

func ReconcileServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, konnectivityImage string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityServerLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityServerLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityServerContainer(), buildKonnectivityServerContainer(konnectivityImage)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeServerCA(), buildKonnectivityVolumeServerCA),
					util.BuildVolume(konnectivityVolumeServerCerts(), buildKonnectivityVolumeServerCerts),
					util.BuildVolume(konnectivityVolumeClusterCerts(), buildKonnectivityVolumeClusterCerts),
					util.BuildVolume(konnectivityVolumeKubeconfig(), buildKonnectivityVolumeKubeconfig),
				},
			},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func konnectivityServerContainer() *corev1.Container {
	return &corev1.Container{
		Name: "konnectivity-server",
	}
}

func buildKonnectivityServerContainer(image string) func(c *corev1.Container) {
	cpath := func(volume, file string) string {
		return path.Join(volumeMounts.Path(konnectivityServerContainer().Name, volume), file)
	}
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Command = []string{
			"/proxy-server",
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
			cpath(konnectivityVolumeServerCA().Name, pki.CASignerCertMapKey),
			"--mode=grpc",
			"--server-port",
			strconv.Itoa(KonnectivityServerLocalPort),
			"--agent-port",
			strconv.Itoa(KonnectivityServerPort),
			"--health-port=8092",
			"--admin-port=8093",
			"--agent-namespace=kube-system",
			"--agent-service-account=konnectivity-agent",
			"--kubeconfig",
			cpath(konnectivityVolumeKubeconfig().Name, KubeconfigKey),
			"--authentication-audience=system:konnectivity-server",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func konnectivityVolumeServerCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-ca",
	}
}

func buildKonnectivityVolumeServerCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.CombinedCAConfigMap("").Name,
		},
	}
}

func konnectivityVolumeServerCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-certs",
	}
}

func buildKonnectivityVolumeServerCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityServerCertSecret("").Name,
	}
}

func konnectivityVolumeClusterCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-certs",
	}
}

func buildKonnectivityVolumeClusterCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityClusterCertSecret("").Name,
	}
}

func konnectivityVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildKonnectivityVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

const (
	KonnectivityServerLocalPort = 8090
	KonnectivityServerPort      = 8091
)

func ReconcileServerLocalService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Spec.Selector = konnectivityServerLabels
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(KonnectivityServerLocalPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(KonnectivityServerLocalPort)
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileServerService(svc *corev1.Service, ownerRef config.OwnerRef, strategy *hyperv1.ServicePublishingStrategy) error {
	ownerRef.ApplyTo(svc)
	svc.Spec.Selector = konnectivityServerLabels
	var portSpec corev1.ServicePort
	if len(svc.Spec.Ports) > 0 {
		portSpec = svc.Spec.Ports[0]
	} else {
		svc.Spec.Ports = []corev1.ServicePort{portSpec}
	}
	portSpec.Port = int32(KonnectivityServerPort)
	portSpec.Protocol = corev1.ProtocolTCP
	portSpec.TargetPort = intstr.FromInt(KonnectivityServerPort)
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	default:
		return fmt.Errorf("invalid publishing strategy for Konnectivity service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileServerServiceStatus(svc *corev1.Service, strategy *hyperv1.ServicePublishingStrategy) (host string, port int32, err error) {
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return
		}
		switch {
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
			port = int32(KonnectivityServerPort)
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			host = svc.Status.LoadBalancer.Ingress[0].IP
			port = int32(KonnectivityServerPort)
		}
	case hyperv1.NodePort:
		if strategy.NodePort == nil {
			err = fmt.Errorf("strategy details not specified for Konnectivity nodeport type service")
			return
		}
		if len(svc.Spec.Ports) == 0 {
			return
		}
		if svc.Spec.Ports[0].NodePort == 0 {
			return
		}
		port = svc.Spec.Ports[0].NodePort
		host = strategy.NodePort.Address
	}
	return
}
