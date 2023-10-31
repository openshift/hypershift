package ingress

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	routerConfigKey     = "haproxy.cfg"
	routerConfigHashKey = "hypershift.openshift.io/config-hash"
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
					corev1.ResourceMemory: resource.MustParse("40Mi"),
					corev1.ResourceCPU:    resource.MustParse("50m"),
				},
			},
		},
	}

	cfg.Scheduling.PriorityClass = config.APICriticalPriorityClass
	cfg.SetRequestServingDefaults(hcp, hcpRouterLabels(), nil)
	cfg.SetRestartAnnotation(hcp.ObjectMeta)
	cfg.SetDefaultSecurityContext = setDefaultSecurityContext
	return cfg
}

const PrivateRouterImage = "haproxy-router"

//go:embed router_config.template
var routerConfigTemplateStr string
var routerConfigTemplate *template.Template

func init() {
	var err error
	routerConfigTemplate, err = template.New("router-config").Parse(routerConfigTemplateStr)
	if err != nil {
		panic(err.Error())
	}
}

type byRouteName []routev1.Route

func (r byRouteName) Len() int           { return len(r) }
func (r byRouteName) Swap(i, j int)      { r[i], r[j] = r[j], r[i] }
func (r byRouteName) Less(i, j int) bool { return r[i].Name < r[j].Name }

func generateRouterConfig(namespace string, kubeAPIPort int32, routeList *routev1.RouteList, nameServerIP string) (string, error) {
	type backendDesc struct {
		Name               string
		HostName           string
		DestinationService string
		DestinationPort    int32
	}
	type templateParams struct {
		HasKubeAPI   bool
		Namespace    string
		KubeAPIPort  int32
		Backends     []backendDesc
		NameServerIP string
	}
	p := templateParams{
		Namespace:    namespace,
		NameServerIP: nameServerIP,
	}
	sort.Sort(byRouteName(routeList.Items))
	for _, route := range routeList.Items {
		if _, hasHCPLabel := route.Labels[util.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		switch route.Name {
		case manifests.KubeAPIServerInternalRoute("").Name,
			manifests.KubeAPIServerExternalPublicRoute("").Name,
			manifests.KubeAPIServerExternalPrivateRoute("").Name:
			p.HasKubeAPI = true
			continue
		case ignitionserver.Route("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "ignition", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: 443})
		case manifests.KonnectivityServerRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "konnectivity", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: 8091})
		case manifests.OauthServerExternalPrivateRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth_private", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: 6443})
		case manifests.OauthServerExternalPublicRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: 6443})
		case manifests.OauthServerInternalRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "oauth_internal", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: 6443})
		case manifests.OVNKubeSBDBRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "ovnkube_sbdb", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: route.Spec.Port.TargetPort.IntVal})
		case manifests.MetricsForwarderRoute("").Name:
			p.Backends = append(p.Backends, backendDesc{Name: "metrics_forwarder", HostName: route.Spec.Host, DestinationService: route.Spec.To.Name, DestinationPort: route.Spec.Port.TargetPort.IntVal})
		}
	}
	if p.HasKubeAPI {
		p.KubeAPIPort = kubeAPIPort
	}
	out := &bytes.Buffer{}
	if err := routerConfigTemplate.Execute(out, p); err != nil {
		return "", fmt.Errorf("failed to generate router config: %w", err)
	}
	return out.String(), nil
}

func ReconcileRouterConfiguration(ownerRef config.OwnerRef, cm *corev1.ConfigMap, kubeAPIPort int32, routeList *routev1.RouteList, nameServerIP string) error {
	ownerRef.ApplyTo(cm)

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	routerConfig, err := generateRouterConfig(cm.Namespace, kubeAPIPort, routeList, nameServerIP)
	if err != nil {
		return err
	}
	cm.Data[routerConfigKey] = routerConfig
	return nil
}

func ReconcileRouterDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image string, config *corev1.ConfigMap) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: hcpRouterLabels(),
				Annotations: map[string]string{
					routerConfigHashKey: util.ComputeHash(config.Data[routerConfigKey]),
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(hcpRouterContainerMain(), buildHCPRouterContainerMain(image)),
				},
				Volumes: []corev1.Volume{
					{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: manifests.RouterConfigurationConfigMap("").Name},
							},
						},
					},
				},
				AutomountServiceAccountToken: pointer.Bool(false),
			},
		},
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

func buildHCPRouterContainerMain(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Command = []string{
			"haproxy",
		}
		c.Image = image
		c.Args = []string{
			"-f", "/usr/local/etc/haproxy",
		}
		c.LivenessProbe = &corev1.Probe{
			InitialDelaySeconds: 50,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/haproxy_ready",
					Port: intstr.FromInt(9444),
				},
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				ContainerPort: 8443,
				Name:          "https",
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.SecurityContext = &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"NET_BIND_SERVICE",
				},
			},
		}
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      "config",
			MountPath: "/usr/local/etc/haproxy/haproxy.cfg",
			SubPath:   "haproxy.cfg",
		})
	}
}

func ReconcileRouterService(svc *corev1.Service, internal, crossZoneLoadBalancingEnabled bool) error {
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

	for i, port := range svc.Spec.Ports {
		switch port.Name {
		case "https":
			svc.Spec.Ports[i].Port = 443
			svc.Spec.Ports[i].TargetPort = intstr.FromString("https")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundHTTPS = true
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

	return nil
}

func ReconcileRouteStatus(route *routev1.Route, externalHostname, internalHostname string) {
	var canonicalHostName string
	if _, isInternal := route.Labels[util.InternalRouteLabel]; isInternal {
		canonicalHostName = internalHostname
	} else {
		canonicalHostName = externalHostname
	}

	// Skip reconciliation if ingress status.ingress has already been populated and canonical hostname is the same
	if len(route.Status.Ingress) > 0 && route.Status.Ingress[0].RouterCanonicalHostname == canonicalHostName {
		return
	}

	ingress := routev1.RouteIngress{
		Host:                    route.Spec.Host,
		RouterName:              "router",
		WildcardPolicy:          routev1.WildcardPolicyNone,
		RouterCanonicalHostname: canonicalHostName,
	}

	if len(route.Status.Ingress) > 0 && len(route.Status.Ingress[0].Conditions) > 0 {
		ingress.Conditions = route.Status.Ingress[0].Conditions
	} else {
		now := metav1.Now()
		ingress.Conditions = []routev1.RouteIngressCondition{
			{
				Type:               routev1.RouteAdmitted,
				LastTransitionTime: &now,
				Status:             corev1.ConditionTrue,
			},
		}
	}
	route.Status.Ingress = []routev1.RouteIngress{ingress}
}
