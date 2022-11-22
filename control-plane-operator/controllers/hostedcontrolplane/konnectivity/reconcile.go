package konnectivity

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"k8s.io/utils/pointer"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/util/intstr"

	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		konnectivityServerContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeServerCerts().Name:  "/etc/konnectivity/server",
			konnectivityVolumeClusterCerts().Name: "/etc/konnectivity/cluster",
			konnectivitySignerCA().Name:           "/etc/konnectivity/ca",
		},
		konnectivityAgentContainer().Name: util.ContainerVolumeMounts{
			konnectivityVolumeAgentCerts().Name: "/etc/konnectivity/agent",
			konnectivitySignerCA().Name:         "/etc/konnectivity/ca",
		},
	}
)

func konnectivityServerLabels() map[string]string {
	return map[string]string{
		"app":                         "konnectivity-server",
		hyperv1.ControlPlaneComponent: "konnectivity-server",
	}
}

func konnectivityAgentLabels() map[string]string {
	return map[string]string{
		"app":                         "konnectivity-agent",
		hyperv1.ControlPlaneComponent: "konnectivity-agent",
	}
}

const (
	KubeconfigKey = "kubeconfig"
)

func ReconcileServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityServerLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityServerLabels(),
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityServerContainer(), buildKonnectivityServerContainer(image)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeServerCerts(), buildKonnectivityVolumeServerCerts),
					util.BuildVolume(konnectivityVolumeClusterCerts(), buildKonnectivityVolumeClusterCerts),
					util.BuildVolume(konnectivitySignerCA(), buildKonnectivitySignerCAkonnectivitySignerCAVolume),
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
		c.ImagePullPolicy = corev1.PullIfNotPresent
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
			cpath(konnectivitySignerCA().Name, certs.CASignerCertMapKey),
			"--server-port",
			strconv.Itoa(KonnectivityServerLocalPort),
			"--agent-port",
			strconv.Itoa(KonnectivityServerPort),
			"--health-port",
			strconv.Itoa(healthPort),
			"--admin-port=8093",
			"--mode=http-connect",
			"--proxy-strategies=destHost,defaultRoute",
			"--keepalive-time",
			"30s",
			"--frontend-keepalive-time",
			"30s",
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
		SecretName:  manifests.KonnectivityServerSecret("").Name,
		DefaultMode: pointer.Int32Ptr(0640),
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
		DefaultMode: pointer.Int32Ptr(0640),
	}
}

func konnectivitySignerCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "konnectivity-ca",
	}
}

func buildKonnectivitySignerCAkonnectivitySignerCAVolume(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.KonnectivityCAConfigMap("").Name
}

const (
	KonnectivityServerLocalPort = 8090
	KonnectivityServerPort      = 8091
)

func ReconcileServerLocalService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Spec.Selector = konnectivityServerLabels()
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
	svc.Spec.Selector = konnectivityServerLabels()
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
		if strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "" {
			if svc.Annotations == nil {
				svc.Annotations = map[string]string{}
			}
			svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = strategy.LoadBalancer.Hostname
		}
	case hyperv1.NodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		if portSpec.NodePort == 0 && strategy.NodePort != nil {
			portSpec.NodePort = strategy.NodePort.Port
		}
	case hyperv1.Route:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	default:
		return fmt.Errorf("invalid publishing strategy for Konnectivity service: %s", strategy.Type)
	}
	svc.Spec.Ports[0] = portSpec
	return nil
}

func ReconcileExternalRoute(route *routev1.Route, ownerRef config.OwnerRef, hostname string, defaultIngressDomain string) error {
	ownerRef.ApplyTo(route)
	return util.ReconcileExternalRoute(route, hostname, defaultIngressDomain, manifests.KonnectivityServerService(route.Namespace).Name)
}

func ReconcileInternalRoute(route *routev1.Route, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(route)
	// Assumes ownerRef is the HCP
	return util.ReconcileInternalRoute(route, ownerRef.Reference.Name, manifests.KonnectivityServerService(route.Namespace).Name)
}

func ReconcileServerServiceStatus(svc *corev1.Service, route *routev1.Route, strategy *hyperv1.ServicePublishingStrategy, messageCollector events.MessageCollector) (host string, port int32, message string, err error) {
	switch strategy.Type {
	case hyperv1.LoadBalancer:
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			message = fmt.Sprintf("Konnectivity load balancer is not provisioned; %v since creation", duration.ShortHumanDuration(time.Since(svc.ObjectMeta.CreationTimestamp.Time)))
			var messages []string
			messages, err = messageCollector.ErrorMessages(svc)
			if err != nil {
				err = fmt.Errorf("failed to get events for service %s/%s: %w", svc.Namespace, svc.Name, err)
				return
			}
			if len(messages) > 0 {
				message = fmt.Sprintf("Konnectivity load balancer is not provisioned: %s", strings.Join(messages, "; "))
			}
			return
		}
		port = int32(KonnectivityServerPort)
		switch {
		case strategy.LoadBalancer != nil && strategy.LoadBalancer.Hostname != "":
			host = strategy.LoadBalancer.Hostname
		case svc.Status.LoadBalancer.Ingress[0].Hostname != "":
			host = svc.Status.LoadBalancer.Ingress[0].Hostname
		case svc.Status.LoadBalancer.Ingress[0].IP != "":
			host = svc.Status.LoadBalancer.Ingress[0].IP
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
	case hyperv1.Route:
		if strategy.Route != nil && strategy.Route.Hostname != "" {
			host = strategy.Route.Hostname
			port = 443
			return
		}
		if route.Spec.Host == "" {
			return
		}
		port = 443
		host = route.Spec.Host
	}
	return
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

func buildKonnectivityVolumeAgentCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  manifests.KonnectivityAgentSecret("").Name,
		DefaultMode: pointer.Int32Ptr(0640),
	}
}

func ReconcileAgentDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, ips []string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: konnectivityAgentLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: konnectivityAgentLabels(),
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				Containers: []corev1.Container{
					util.BuildContainer(konnectivityAgentContainer(), buildKonnectivityAgentContainer(image, ips)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(konnectivityVolumeAgentCerts(), buildKonnectivityVolumeAgentCerts),
					util.BuildVolume(konnectivitySignerCA(), buildKonnectivitySignerCAkonnectivitySignerCAVolume),
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
			fmt.Sprint(KonnectivityServerPort),
			"--health-server-port",
			fmt.Sprint(healthPort),
			"--agent-identifiers",
			agentIDs.String(),
			"--keepalive-time",
			"30s",
			"--probe-interval",
			"30s",
			"--sync-interval",
			"1m",
			"--sync-interval-cap",
			"5m",
			"--v",
			"3",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}
