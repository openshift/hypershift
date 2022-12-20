package ingress

import (
	"bytes"
	_ "embed"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	metricsPort     = 1936
	routerHTTPPort  = 8080
	routerHTTPSPort = 8443
)

func hcpRouterLabels() map[string]string {
	return map[string]string{
		"app": "private-router",
	}
}

func HCPRouterConfig(hcp *hyperv1.HostedControlPlane, setDefaultSecurityContext bool) config.DeploymentConfig {
	cfg := config.DeploymentConfig{
		Resources: config.ResourcesSpec{
			hcpRouterContainerMain().Name: {
				Requests: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("256Mi"),
					corev1.ResourceCPU:    resource.MustParse("100m"),
				},
			},
		},
	}
	cfg.LivenessProbes = config.LivenessProbes{
		hcpRouterContainerMain().Name: corev1.Probe{
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
		hcpRouterContainerMain().Name: corev1.Probe{
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
	cfg.SetDefaults(hcp, hcpRouterLabels(), nil)
	cfg.SetRestartAnnotation(hcp.ObjectMeta)
	cfg.SetDefaultSecurityContext = setDefaultSecurityContext
	return cfg
}

func PrivateRouterImage(images map[string]string) string {
	return images["haproxy-router"]
}

const (
	routerTemplateConfigMapKey = "haproxy-config.template"
	routerTemplateVolumeName   = "happroxy-config"
)

func ReconcileRouterTemplateConfigmap(cm *corev1.ConfigMap) {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[routerTemplateConfigMapKey] = string(bytes.Replace(routerTemplate, []byte(`<<namespace>>`), []byte(cm.Namespace), 1))
}

func ReconcileRouterDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, canonicalHostname string, exposeAPIServerThroughRouter bool, isPrivateOnly bool) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: hcpRouterLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(hcpRouterContainerMain(), buildHCPRouterContainerMain(image, deployment.Namespace, canonicalHostname, exposeAPIServerThroughRouter, isPrivateOnly)),
				},
				ServiceAccountName: manifests.RouterServiceAccount("").Name,
				Volumes:            nil,
			},
		},
	}
	if exposeAPIServerThroughRouter {
		deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
			{Name: routerTemplateVolumeName, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: manifests.RouterTemplateConfigMap("").Name}}}},
		}
	}

	ownerRef.ApplyTo(deployment)
	deploymentConfig.ApplyTo(deployment)

	return nil
}

func hcpRouterContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "private-router",
	}
}

func buildHCPRouterContainerMain(image, namespace, canonicalHostname string, exposeAPIServerThroughRouter bool, isPrivateOnly bool) func(*corev1.Container) {
	const haproxyTemplateMountPath = "/usr/local/haproxy/hypershift-template"
	routeLabels := fmt.Sprintf("%s=%s", util.HCPRouteLabel, namespace)
	if isPrivateOnly {
		routeLabels = fmt.Sprintf("%s,%s=%s", routeLabels, util.InternalRouteLabel, "true")
	}
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
				Value: manifests.RouterPublicService("").Name,
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
				Value: routeLabels,
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
			{
				Name:  "ROUTER_CANONICAL_HOSTNAME",
				Value: canonicalHostname,
			},
		}

		c.Image = image
		c.Args = []string{
			"--namespace", namespace,
		}

		if exposeAPIServerThroughRouter {
			c.Args = append(c.Args, "--template="+haproxyTemplateMountPath+"/"+routerTemplateConfigMapKey)
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

		c.VolumeMounts = nil
		if exposeAPIServerThroughRouter {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      routerTemplateVolumeName,
				MountPath: haproxyTemplateMountPath,
			})
		}

		// Needed for the router pods to work: https://github.com/openshift/cluster-ingress-operator/blob/649fe5dfe2c6f795651592a045be901b00a1f93a/assets/router/deployment.yaml#L22-L23
		c.SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: utilpointer.Bool(true)}
	}
}

func ReconcileRouterRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
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
		{
			// Copied from https://github.com/openshift/cluster-ingress-operator/blob/649fe5dfe2c6f795651592a045be901b00a1f93a/manifests/00-cluster-role.yaml#L173-L181
			// Needed to allow PrivilegeEscalation: true
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"hostnetwork"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
	}
	return nil
}

func ReconcileRouterRoleBinding(rb *rbacv1.RoleBinding, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(rb)
	rb.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.RouterServiceAccount("").Name,
		},
	}
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.RouterRole("").Name,
	}
	return nil
}

func ReconcileRouterServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, common.PullSecret("").Name)
	return nil
}

func ReconcileRouterService(svc *corev1.Service, kasPort int32, internal, crossZoneLoadBalancingEnabled bool) error {
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] = "nlb"
	if internal {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
	}
	if crossZoneLoadBalancingEnabled {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	}

	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range hcpRouterLabels() {
		svc.Labels[k] = v
	}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Selector = hcpRouterLabels()
	foundHTTPS := false
	foundKAS := false

	if kasPort == 443 {
		foundKAS = true
	}
	for i, port := range svc.Spec.Ports {
		switch port.Name {
		case "https":
			svc.Spec.Ports[i].Port = 443
			svc.Spec.Ports[i].TargetPort = intstr.FromString("https")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundHTTPS = true
		case "kube-apiserver":
			svc.Spec.Ports[i].Port = kasPort
			svc.Spec.Ports[i].TargetPort = intstr.FromString("https")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundKAS = true
		}
	}
	if !foundHTTPS {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "https",
			Port:       443,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	if !foundKAS {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "kube-apiserver",
			Port:       kasPort,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return nil
}

//go:embed router.template
var routerTemplate []byte
