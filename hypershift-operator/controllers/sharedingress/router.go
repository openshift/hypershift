package sharedingress

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"text/template"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	routerConfigKey     = "haproxy.cfg"
	routerConfigHashKey = "hypershift.openshift.io/config-hash"
	KASSVCLBPort        = 6443
	ExternalDNSLBPort   = 443
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

func generateRouterConfig(svcList *corev1.ServiceList, svcsNamespaceToClusterID map[string]string, routes []routev1.Route,
	svcsNameToIP map[string]string, svcsNamespaceToApiServerNetworking map[string]*hyperv1.APIServerNetworking) (string, error) {
	type backendDesc struct {
		Name         string
		SVCIP        string
		SVCPort      int32
		ClusterID    string
		AllowedCIDRs []hyperv1.CIDRBlock
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
			Name:         svc.Namespace + "-" + svc.Name,
			SVCIP:        svc.Spec.ClusterIP,
			SVCPort:      svc.Spec.Ports[0].Port,
			ClusterID:    svcsNamespaceToClusterID[svc.Namespace],
			AllowedCIDRs: setAllowedCIDRs(svcsNamespaceToApiServerNetworking[svc.Namespace]),
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
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{
				Name:                 route.Namespace + "-apiserver",
				HostName:             route.Spec.Host,
				DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name],
				DestinationPort:      config.KASSVCPort})
		case ignitionserver.Route("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{
				Name:                 route.Namespace + "-ignition",
				HostName:             route.Spec.Host,
				DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name],
				DestinationPort:      443})
		case manifests.KonnectivityServerRoute("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{
				Name:                 route.Namespace + "-konnectivity",
				HostName:             route.Spec.Host,
				DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name],
				DestinationPort:      8091})
		case manifests.OauthServerExternalPublicRoute("").Name:
			p.ExternalDNSBackends = append(p.ExternalDNSBackends, ExternalDNSBackendDesc{
				Name:                 route.Namespace + "-oauth",
				HostName:             route.Spec.Host,
				DestinationServiceIP: svcsNameToIP[route.Namespace+route.Spec.To.Name],
				DestinationPort:      6443})
		}
	}

	out := &bytes.Buffer{}
	if err := routerConfigTemplate.Execute(out, p); err != nil {
		return "", fmt.Errorf("failed to generate router config: %w", err)
	}
	return out.String(), nil
}

func setAllowedCIDRs(apiServerNetworking *hyperv1.APIServerNetworking) []hyperv1.CIDRBlock {
	if apiServerNetworking == nil || len(apiServerNetworking.AllowedCIDRBlocks) == 0 {
		return []hyperv1.CIDRBlock{}
	}
	return apiServerNetworking.AllowedCIDRBlocks
}

func ReconcileRouterConfiguration(cm *corev1.ConfigMap, config string) error {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data[routerConfigKey] = config
	return nil
}

func ReconcileRouterDeployment(deployment *appsv1.Deployment, configMap *corev1.ConfigMap) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](2),
		Selector: &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: hcpRouterLabels(),
				Annotations: map[string]string{
					routerConfigHashKey: util.ComputeHash(configMap.Data[routerConfigKey]),
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
				AutomountServiceAccountToken: ptr.To(false),
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									TopologyKey: corev1.LabelTopologyZone,
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: hcpRouterLabels(),
									},
								},
							},
						},
					},
				},
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

		// proxy protocol v2 with TLV support (custom proxy protocol header) requires haproxy v2.9+, see: https://www.haproxy.com/blog/announcing-haproxy-2-9#proxy-protocol-tlv-fields
		// TODO: get the image from the payload once available https://issues.redhat.com/browse/HOSTEDCP-1819
		c.Image = "quay.io/rh_ee_brcox/hypershift:haproxy2.9.9-multi"
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
	svc.Spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyLocal
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
			TargetPort: intstr.FromString("external-dns"),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	if !foundKASSVC {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "kas-svc",
			Port:       KASSVCLBPort,
			TargetPort: intstr.FromString("kas-svc"),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	return nil
}

func ReconcileRouterPodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, ownerRef config.OwnerRef) {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		}
	}
	ownerRef.ApplyTo(pdb)
	pdb.Spec.MinAvailable = ptr.To(intstr.FromInt32(1))
}

func ReconcileRouterNetworkPolicy(policy *networkingv1.NetworkPolicy, isOpenShiftDNS bool, managementClusterNetwork *configv1.Network) {
	httpPort := intstr.FromInt(8080)
	httpsPort := intstr.FromInt(8443)
	protocol := corev1.ProtocolTCP

	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: hcpRouterLabels(),
	}

	// Allow ingress to the router ports
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &httpPort,
					Protocol: &protocol,
				},
				{
					Port:     &httpsPort,
					Protocol: &protocol,
				},
			},
		},
	}

	clusterNetworks := make([]string, 0)
	// In vanilla kube management cluster this would be nil.
	if managementClusterNetwork != nil {
		for _, network := range managementClusterNetwork.Spec.ClusterNetwork {
			clusterNetworks = append(clusterNetworks, network.CIDR)
		}
	}

	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			// Allow traffic to the internet (excluding cluster internal IPs).
			// This is needed for the router to be able to resolve external DNS names.
			To: []networkingv1.NetworkPolicyPeer{
				{
					IPBlock: &networkingv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: clusterNetworks,
					},
				},
			},
		},
		{
			// Allow traffic to the HostedControlPlane kube-apiserver in all namespaces.
			To: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app":                              "kube-apiserver",
							hyperv1.ControlPlaneComponentLabel: "kube-apiserver",
						},
					},
				},
			},
		},
	}

	if isOpenShiftDNS {
		// Allow traffic to openshift-dns namespace
		dnsUDPPort := intstr.FromInt(5353)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(5353)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": "openshift-dns",
						},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	} else {
		// All traffic to any destination on port 53 for both TCP and UDP
		dnsUDPPort := intstr.FromInt(53)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(53)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	}

	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
}
