package konnectivity

import (
	"bytes"
	"fmt"
	"k8s.io/utils/pointer"
	"path"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		konnectivityServerContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeServerCerts().Name:  "/etc/konnectivity/server",
			konnectivityVolumeClusterCerts().Name: "/etc/konnectivity/cluster",
		},
		konnectivityAgentContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeAgentCerts().Name: "/etc/konnectivity/agent",
		},
	}
	konnectivityServerLabels = map[string]string{
		"app": "konnectivity-server",
	}
	konnectivityAgentLabels = map[string]string{
		"app": "konnectivity-agent",
	}
)

const (
	KubeconfigKey = "kubeconfig"
)

func ReconcileServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string) error {
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
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityServerContainer(), buildKonnectivityServerContainer(image)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeServerCerts(), buildKonnectivityVolumeServerCerts),
					util.BuildVolume(konnectivityVolumeClusterCerts(), buildKonnectivityVolumeClusterCerts),
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
			cpath(konnectivityVolumeServerCerts().Name, pki.CASignerCertMapKey),
			"--mode=grpc",
			"--server-port",
			strconv.Itoa(KonnectivityServerLocalPort),
			"--agent-port",
			strconv.Itoa(KonnectivityServerPort),
			"--health-port=8092",
			"--admin-port=8093",
			"--mode=http-connect",
			"--proxy-strategies=destHost,defaultRoute",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func konnectivityVolumeServerCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-certs",
	}
}

func buildKonnectivityVolumeServerCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityServerSecret("").Name,
	}
}

func konnectivityVolumeClusterCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-certs",
	}
}

func buildKonnectivityVolumeClusterCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityClusterSecret("").Name,
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

func ReconcileWorkerAgentDaemonSet(cm *corev1.ConfigMap, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, host string, port int32) error {
	ownerRef.ApplyTo(cm)
	agentDaemonSet := manifests.KonnectivityAgentDaemonSet()
	if err := reconcileWorkerAgentDaemonSet(agentDaemonSet, deploymentConfig, image, host, port); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, agentDaemonSet)
}

func reconcileWorkerAgentDaemonSet(daemonset *appsv1.DaemonSet, deploymentConfig config.DeploymentConfig, image string, host string, port int32) error {
	daemonset.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityAgentLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityAgentLabels,
			},
			Spec: corev1.PodSpec{
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
	deploymentConfig.ApplyToDaemonSet(daemonset)
	return nil
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

func buildKonnectivityVolumeWorkerAgentCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KonnectivityAgentSecret("").Name,
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
			cpath(konnectivityVolumeAgentCerts().Name, pki.CASignerCertMapKey),
			"--agent-cert",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSCertKey),
			"--agent-key",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSPrivateKeyKey),
			"--proxy-server-host",
			host,
			"--proxy-server-port",
			fmt.Sprint(port),
			"--agent-identifiers=default-route=true",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func ReconcileAgentDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, ips []string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityAgentLabels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityAgentLabels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityAgentContainer(image, ips)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeWorkerAgentCerts),
				},
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
	seperator := ""
	for i, ip := range ips {
		agentIDs.WriteString(fmt.Sprintf("%sipv4=%s", seperator, ip))
		if i == 0 {
			seperator = "&"
		}
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
			cpath(konnectivityVolumeAgentCerts().Name, pki.CASignerCertMapKey),
			"--agent-cert",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSCertKey),
			"--agent-key",
			cpath(konnectivityVolumeAgentCerts().Name, corev1.TLSPrivateKeyKey),
			"--proxy-server-host",
			manifests.KonnectivityServerService("").Name,
			"--proxy-server-port",
			fmt.Sprint(KonnectivityServerPort),
			"--agent-identifiers",
			agentIDs.String(),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}
