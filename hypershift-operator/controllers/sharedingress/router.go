package sharedingress

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"text/template"

	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

const (
	routerConfigKey     = "haproxy.cfg"
	routerConfigHashKey = "hypershift.openshift.io/config-hash"
	KASSVCLBPort        = 6443
	// For Azure or Kubevirt on Azure we currently hardcode 7443 for the SVC LB as 6443 collides with public LB rule for the management cluster.
	// https://bugzilla.redhat.com/show_bug.cgi?id=2060650
	ExternalDNSLBPort = 7443
)

func hcpRouterLabels() map[string]string {
	return map[string]string{
		"app": "router",
	}
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

type svcByNamespace []corev1.Service

func (s svcByNamespace) Len() int           { return len(s) }
func (s svcByNamespace) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s svcByNamespace) Less(i, j int) bool { return s[i].Namespace < s[j].Namespace }

type routeByNamespaceName []routev1.Route

func (r routeByNamespaceName) Len() int      { return len(r) }
func (r routeByNamespaceName) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
func (r routeByNamespaceName) Less(i, j int) bool {
	return r[i].Namespace+r[i].Name < r[j].Namespace+r[j].Name
}

func generateRouterConfig(svcList *corev1.ServiceList, routes []routev1.Route, svcsNameToIP map[string]string) (string, error) {
	type backendDesc struct {
		Name    string
		SVCIP   string
		SVCPort int32
	}
	type ExternalDNSBackendDesc struct {
		Name                 string
		HostName             string
		DestinationServiceIP string
		DestinationPort      int32
	}
	type templateParams struct {
		Backends            []backendDesc
		ExternalDNSBackends []ExternalDNSBackendDesc
	}
	p := templateParams{}
	sort.Sort(svcByNamespace(svcList.Items))
	p.Backends = make([]backendDesc, 0, len(svcList.Items))
	for _, svc := range svcList.Items {
		p.Backends = append(p.Backends, backendDesc{
			Name:    svc.Namespace + "-" + svc.Name,
			SVCIP:   svc.Spec.ClusterIP,
			SVCPort: svc.Spec.Ports[0].Port,
		})
	}

	sort.Sort(routeByNamespaceName(routes))
	p.ExternalDNSBackends = make([]ExternalDNSBackendDesc, 0, len(routes))
	for _, route := range routes {
		if _, hasHCPLabel := route.Labels[util.HCPRouteLabel]; !hasHCPLabel {
			// If the hypershift.openshift.io/hosted-control-plane label is not present,
			// then it means the route should be fulfilled by the management cluster's router.
			continue
		}
		switch route.Name {
		case manifests.KubeAPIServerExternalPublicRoute("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{Name: route.Namespace + "-apiserver", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name], DestinationPort: config.KASSVCPort})
		case ignitionserver.Route("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{Name: route.Namespace + "-ignition", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name], DestinationPort: 443})
		case manifests.KonnectivityServerRoute("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{Name: route.Namespace + "-konnectivity", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name], DestinationPort: 8091})
		case manifests.OauthServerExternalPublicRoute("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{Name: route.Namespace + "-oauth", HostName: route.Spec.Host, DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name], DestinationPort: 6443})
		}
	}

	out := &bytes.Buffer{}
	if err := routerConfigTemplate.Execute(out, p); err != nil {
		return "", fmt.Errorf("failed to generate router config: %w", err)
	}
	return out.String(), nil
}

func ReconcileRouterConfiguration(cm *corev1.ConfigMap, config string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data[routerConfigKey] = config
	return nil
}

func ReconcileRouterDeployment(deployment *appsv1.Deployment, config *corev1.ConfigMap) error {
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
					util.BuildContainer(hcpRouterContainerMain(), buildHCPRouterContainerMain()),
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
				ServiceAccountName:           "",
				AutomountServiceAccountToken: pointer.Bool(false),
			},
		},
	}

	return nil
}

func hcpRouterContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "private-router",
	}
}

func buildHCPRouterContainerMain() func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Command = []string{
			"haproxy",
		}
		// 4.16.0-rc.3 haproxy router image.
		c.Image = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:06e8c4156888f50be0ac46bf21337ecda329befa0f9a6bb1df8bce078ae04aaa"
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
				Name:          "external-dns",
				Protocol:      corev1.ProtocolTCP,
			},
			{
				ContainerPort: 6443,
				Name:          "kas-svc",
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

func ReconcileRouterService(svc *corev1.Service) error {
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range hcpRouterLabels() {
		svc.Labels[k] = v
	}
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Spec.Selector = hcpRouterLabels()
	foundExternaDNS := false
	foundKASSVC := false

	for i, port := range svc.Spec.Ports {
		switch port.Name {
		case "external-dns":
			svc.Spec.Ports[i].TargetPort = intstr.FromString("external-dns")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundExternaDNS = true
		case "kas-svc":
			svc.Spec.Ports[i].Port = KASSVCLBPort
			svc.Spec.Ports[i].TargetPort = intstr.FromString("kas-svc")
			svc.Spec.Ports[i].Protocol = corev1.ProtocolTCP
			foundKASSVC = true
		}
	}
	if !foundExternaDNS {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "external-dns",
			Port:       ExternalDNSLBPort,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	if !foundKASSVC {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "kas-svc",
			Port:       KASSVCLBPort,
			TargetPort: intstr.FromString("https"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	return nil
}

func ReconcileRouterPodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, availability hyperv1.AvailabilityPolicy, ownerRef config.OwnerRef) {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		}
	}
	ownerRef.ApplyTo(pdb)
	util.ReconcilePodDisruptionBudget(pdb, availability)
}
