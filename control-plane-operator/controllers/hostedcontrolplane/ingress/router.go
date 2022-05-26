package ingress

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	HypershiftRouteLabel = "hypershift.openshift.io/hosted-control-plane"
	metricsPort          = 1936
	routerHTTPPort       = 8080
	routerHTTPSPort      = 8443
)

func privateRouterLabels() map[string]string {
	return map[string]string{
		"app": "private-router",
	}
}

func PrivateRouterConfig(hcp *hyperv1.HostedControlPlane, setDefaultSecurityContext bool) config.DeploymentConfig {
	cfg := config.DeploymentConfig{
		Resources: config.ResourcesSpec{
			privateRouterContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("256Mi"),
					corev1.ResourceCPU:    resource.MustParse("100m"),
				},
			},
		},
	}
	cfg.LivenessProbes = config.LivenessProbes{
		privateRouterContainerMain().Name: corev1.Probe{
			FailureThreshold: 3,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(metricsPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			TimeoutSeconds:   1,
		},
	}
	cfg.ReadinessProbes = config.ReadinessProbes{
		privateRouterContainerMain().Name: corev1.Probe{
			FailureThreshold: 3,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz/ready",
					Port:   intstr.FromInt(metricsPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			TimeoutSeconds:   1,
		},
	}
	cfg.Scheduling.PriorityClass = config.APICriticalPriorityClass
	cfg.SetColocation(hcp)
	cfg.SetRestartAnnotation(hcp.ObjectMeta)
	cfg.SetReleaseImageAnnotation(hcp.Spec.ReleaseImage)
	cfg.SetControlPlaneIsolation(hcp)
	cfg.SetMultizoneSpread(privateRouterLabels())
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		cfg.Replicas = 3
	default:
		cfg.Replicas = 1
	}
	cfg.SetDefaultSecurityContext = setDefaultSecurityContext
	return cfg
}

func PrivateRouterImage(images map[string]string) string {
	return images["haproxy-router"]
}

func ReconcilePrivateRouterDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image, domain string) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: privateRouterLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: privateRouterLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(privateRouterContainerMain(), buildPrivateRouterContainerMain(image, deployment.Namespace)),
				},
				ServiceAccountName: manifests.PrivateRouterServiceAccount("").Name,
			},
		},
	}
	ownerRef.ApplyTo(deployment)
	deploymentConfig.ApplyTo(deployment)

	return nil
}

func privateRouterContainerMain() *corev1.Container {

	return &corev1.Container{
		Name: "private-router",
	}
}

func buildPrivateRouterContainerMain(image, namespace string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Env = []corev1.EnvVar{
			{
				Name:  "RELOAD_INTERVAL",
				Value: "5s",
			},
			{
				Name:  "ROUTER_ALLOW_WILDCARD_ROUTES",
				Value: "false",
			},
			{
				Name:  "ROUTER_CIPHERS",
				Value: "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384",
			},
			{
				Name:  "ROUTER_CIPHERSUITES",
				Value: "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256",
			},
			{
				Name:  "ROUTER_DISABLE_HTTP2",
				Value: "true",
			},
			{
				Name:  "ROUTER_DISABLE_NAMESPACE_OWNERSHIP_CHECK",
				Value: "true",
			},
			{
				Name:  "ROUTER_LOAD_BALANCE_ALGORITHM",
				Value: "leastconn",
			},
			{
				Name:  "ROUTER_METRICS_TYPE",
				Value: "haproxy",
			},
			{
				Name:  "ROUTER_SERVICE_NAME",
				Value: manifests.PrivateRouterService("").Name,
			},
			{
				Name:  "ROUTER_SERVICE_NAMESPACE",
				Value: namespace,
			},
			{
				Name:  "ROUTER_SET_FORWARDED_HEADERS",
				Value: "append",
			},
			{
				Name:  "ROUTER_TCP_BALANCE_SCHEME",
				Value: "source",
			},
			{
				Name:  "ROUTER_THREADS",
				Value: "4",
			},
			{
				Name:  "ROUTE_LABELS",
				Value: fmt.Sprintf("%s=%s", HypershiftRouteLabel, namespace),
			},
			{
				Name:  "SSL_MIN_VERSION",
				Value: "TLSv1.2",
			},
			{
				Name:  "ROUTER_SERVICE_HTTPS_PORT",
				Value: fmt.Sprintf("%d", routerHTTPSPort),
			},
			{
				Name:  "ROUTER_SERVICE_HTTP_PORT",
				Value: fmt.Sprintf("%d", routerHTTPPort),
			},
			{
				Name:  "STATS_PORT",
				Value: fmt.Sprintf("%d", metricsPort),
			},
		}
		c.Image = image
		c.Args = []string{
			"--namespace", namespace,
		}
		c.StartupProbe = &corev1.Probe{
			FailureThreshold: 120,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz/ready",
					Port:   intstr.FromInt(metricsPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			PeriodSeconds:    1,
			SuccessThreshold: 1,
			TimeoutSeconds:   1,
		}
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: routerHTTPPort,
				Name:          "http",
				Protocol:      corev1.ProtocolTCP,
			},
			{
				ContainerPort: routerHTTPSPort,
				Name:          "https",
				Protocol:      corev1.ProtocolTCP,
			},
			{
				ContainerPort: metricsPort,
				Name:          "metrics",
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}
}

func ReconcilePrivateRouterRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(role)
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"endpoints",
				"services",
			},
			Verbs: []string{
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{
				"route.openshift.io",
			},
			Resources: []string{
				"routes",
			},
			Verbs: []string{
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{
				"route.openshift.io",
			},
			Resources: []string{
				"routes/status",
			},
			Verbs: []string{
				"update",
			},
		},
		{
			APIGroups: []string{
				"discovery.k8s.io",
			},
			Resources: []string{
				"endpointslices",
			},
			Verbs: []string{
				"list",
				"watch",
			},
		},
	}
	return nil
}

func ReconcilePrivateRouterRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.PrivateRouterServiceAccount("").Name,
		},
	}
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.PrivateRouterRole("").Name,
	}
	return nil
}

func ReconcilePrivateRouterServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcilePrivateRouterService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"

	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range privateRouterLabels() {
		svc.Labels[k] = v
	}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Selector = privateRouterLabels()
	foundHTTP := false
	foundHTTPS := false
	for i, port := range svc.Spec.Ports {
		switch port.Name {
		case "http":
			svc.Spec.Ports[i].Port = 80
			svc.Spec.Ports[i].TargetPort = intstr.FromString("http")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundHTTP = true
		case "https":
			svc.Spec.Ports[i].Port = 443
			svc.Spec.Ports[i].TargetPort = intstr.FromString("https")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundHTTPS = true
		}
	}
	if !foundHTTP {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{

			Name:       "http",
			Port:       80,
			TargetPort: intstr.FromString("http"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	if !foundHTTPS {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "https",
			Port:       443,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return nil
}
