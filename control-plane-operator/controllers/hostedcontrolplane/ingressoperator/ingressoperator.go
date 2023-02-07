package ingressoperator

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/konnectivity"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
)

const (
	operatorName                 = "ingress-operator"
	ingressOperatorContainerName = "ingress-operator"
	metricsHostname              = "ingress-operator"
	socks5ProxyContainerName     = "socks-proxy"
	ingressOperatorMetricsPort   = 60000
)

type Params struct {
	IngressOperatorImage    string
	HAProxyRouterImage      string
	KubeRBACProxyImage      string
	ReleaseVersion          string
	TokenMinterImage        string
	AvailabilityProberImage string
	Socks5ProxyImage        string
	Platform                hyperv1.PlatformType
	DeploymentConfig        config.DeploymentConfig
}

func NewParams(hcp *hyperv1.HostedControlPlane, version string, images map[string]string, setDefaultSecurityContext bool, platform hyperv1.PlatformType) Params {
	p := Params{
		IngressOperatorImage:    images["cluster-ingress-operator"],
		HAProxyRouterImage:      images["haproxy-router"],
		ReleaseVersion:          version,
		TokenMinterImage:        images["token-minter"],
		Socks5ProxyImage:        images["socks5-proxy"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		Platform:                platform,
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	p.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	p.DeploymentConfig.ReadinessProbes = config.ReadinessProbes{
		ingressOperatorContainerName: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/metrics",
					Port:   intstr.FromInt(ingressOperatorMetricsPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    3,
			TimeoutSeconds:      5,
		},
	}
	p.DeploymentConfig.LivenessProbes = config.LivenessProbes{
		ingressOperatorContainerName: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/metrics",
					Port:   intstr.FromInt(ingressOperatorMetricsPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 60,
			PeriodSeconds:       60,
			SuccessThreshold:    1,
			FailureThreshold:    5,
			TimeoutSeconds:      5,
		},
	}

	return p
}

func ReconcileDeployment(dep *appsv1.Deployment, params Params, apiPort *int32) {
	dep.Spec.Replicas = utilpointer.Int32(1)
	dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"name": operatorName}}
	dep.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
	if dep.Spec.Template.Labels == nil {
		dep.Spec.Template.Labels = map[string]string{}
	}
	dep.Spec.Template.Labels = map[string]string{
		"name":                        operatorName,
		"app":                         operatorName,
		hyperv1.ControlPlaneComponent: operatorName,
	}

	dep.Spec.Template.Spec.AutomountServiceAccountToken = utilpointer.BoolPtr(false)
	dep.Spec.Template.Spec.Containers = []corev1.Container{{
		Command: []string{
			"ingress-operator",
			"start",
			"--namespace",
			"openshift-ingress-operator",
			"--image",
			"$(IMAGE)",
			"--canary-image",
			"$(CANARY_IMAGE)",
			"--release-version",
			"$(RELEASE_VERSION)",
			"--metrics-listen-addr",
			fmt.Sprintf("0.0.0.0:%d", ingressOperatorMetricsPort),
		},
		Env: []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: params.ReleaseVersion},
			{Name: "IMAGE", Value: params.HAProxyRouterImage},
			{Name: "CANARY_IMAGE", Value: params.IngressOperatorImage},
			{Name: "KUBECONFIG", Value: "/etc/kubernetes/kubeconfig"},
			{
				Name:  "HTTP_PROXY",
				Value: fmt.Sprintf("socks5://127.0.0.1:%d", konnectivity.KonnectivityServerLocalPort),
			},
			{
				Name:  "HTTPS_PROXY",
				Value: fmt.Sprintf("socks5://127.0.0.1:%d", konnectivity.KonnectivityServerLocalPort),
			},
			{
				Name:  "NO_PROXY",
				Value: manifests.KubeAPIServerService("").Name,
			},
		},
		Name:            ingressOperatorContainerName,
		Image:           params.IngressOperatorImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("80Mi"),
		}},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "ingress-operator-kubeconfig", MountPath: "/etc/kubernetes"},
		},
	}}
	dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, ingressOperatorSocks5ProxyContainer(params.Socks5ProxyImage))
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: "ingress-operator-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.IngressOperatorKubeconfig("").Name, DefaultMode: utilpointer.Int32Ptr(0640)}}},
		{Name: "admin-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "service-network-admin-kubeconfig", DefaultMode: utilpointer.Int32Ptr(0640)}}},
		{Name: "konnectivity-proxy-cert", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: manifests.KonnectivityClientSecret("").Name, DefaultMode: utilpointer.Int32Ptr(0640)}}},
		{Name: "konnectivity-proxy-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.KonnectivityCAConfigMap("").Name}, DefaultMode: utilpointer.Int32Ptr(0640)}}},
	}

	if params.Platform == hyperv1.AWSPlatform {
		dep.Spec.Template.Spec.Containers[0].VolumeMounts = append(dep.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{Name: "serviceaccount-token", MountPath: "/var/run/secrets/openshift/serviceaccount"},
		)
		dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, corev1.Container{
			Name:    "token-minter",
			Command: []string{"/usr/bin/control-plane-operator", "token-minter"},
			Args: []string{
				"--service-account-namespace=openshift-ingress-operator",
				"--service-account-name=ingress-operator",
				"--token-file=/var/run/secrets/openshift/serviceaccount/token",
				"--kubeconfig=/etc/kubernetes/kubeconfig",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
			},
			Image: params.TokenMinterImage,
			VolumeMounts: []corev1.VolumeMount{
				{Name: "serviceaccount-token", MountPath: "/var/run/secrets/openshift/serviceaccount"},
				{Name: "admin-kubeconfig", MountPath: "/etc/kubernetes"},
			},
			Ports: []corev1.ContainerPort{
				{
					ContainerPort: ingressOperatorMetricsPort,
					Name:          "metrics",
				},
			},
		})
		dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes,
			corev1.Volume{Name: "serviceaccount-token", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	}

	util.AvailabilityProber(
		kas.InClusterKASReadyURL(dep.Namespace, apiPort),
		params.AvailabilityProberImage,
		&dep.Spec.Template.Spec,
		func(o *util.AvailabilityProberOpts) {
			o.KubeconfigVolumeName = "ingress-operator-kubeconfig"
			o.RequiredAPIs = []schema.GroupVersionKind{
				{Group: "route.openshift.io", Version: "v1", Kind: "Route"},
			}
		},
	)

	params.DeploymentConfig.ApplyTo(dep)
}

func ingressOperatorSocks5ProxyContainer(socks5ProxyImage string) corev1.Container {
	c := corev1.Container{
		Name:    socks5ProxyContainerName,
		Image:   socks5ProxyImage,
		Command: []string{"/usr/bin/control-plane-operator", "konnectivity-socks5-proxy", "--resolve-from-guest-cluster-dns=true"},
		Args: []string{
			"run",
			// Do not route cloud provider traffic through konnektivity and thus nodes to speed
			// up cluster creation. Requires proxy env vars to be set.
			"--connect-directly-to-cloud-apis=true",
		},
		Env: []corev1.EnvVar{{
			Name:  "KUBECONFIG",
			Value: "/etc/kubernetes/kubeconfig",
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "admin-kubeconfig", MountPath: "/etc/kubernetes"},
			{Name: "konnectivity-proxy-cert", MountPath: "/etc/konnectivity/proxy-client"},
			{Name: "konnectivity-proxy-ca", MountPath: "/etc/konnectivity/proxy-ca"},
		},
	}
	proxy.SetEnvVars(&c.Env)
	return c
}

func ReconcilePodMonitor(pm *prometheusoperatorv1.PodMonitor, clusterID string, metricsSet metrics.MetricsSet) {
	pm.Spec.Selector.MatchLabels = map[string]string{
		"name": operatorName,
	}
	pm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{pm.Namespace},
	}
	pm.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{
		{
			Interval:             "60s",
			Port:                 "metrics",
			Path:                 "/metrics",
			Scheme:               "http",
			MetricRelabelConfigs: metrics.RegistryOperatorRelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabelToPodMonitor(&pm.Spec.PodMetricsEndpoints[0], clusterID)
}
