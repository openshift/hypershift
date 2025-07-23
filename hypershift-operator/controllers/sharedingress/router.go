package sharedingress

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"slices"
	"strings"
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

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

type backendDesc struct {
	Name         string
	SVCIP        string
	SVCPort      int32
	ClusterID    string
	AllowedCIDRs string
}
type ExternalDNSBackendDesc struct {
	Name         string
	HostName     string
	SVCIP        string
	SVCPort      int32
	AllowedCIDRs string
}

func generateRouterConfig(ctx context.Context, client crclient.Client) (string, error) {
	logger := log.FromContext(ctx)

	type templateParams struct {
		Backends            []backendDesc
		ExternalDNSBackends []ExternalDNSBackendDesc
	}

	hcList := &hyperv1.HostedClusterList{}
	if err := client.List(ctx, hcList); err != nil {
		return "", fmt.Errorf("failed to list HostedClusters: %w", err)
	}

	hostedClusters := hcList.Items
	slices.SortFunc(hostedClusters, func(a, b hyperv1.HostedCluster) int {
		hcpNamespaceA := a.Namespace + "-" + a.Name
		hcpNamespaceB := b.Namespace + "-" + b.Name
		return strings.Compare(hcpNamespaceA, hcpNamespaceB)
	})

	p := templateParams{
		Backends:            make([]backendDesc, 0, len(hostedClusters)),
		ExternalDNSBackends: make([]ExternalDNSBackendDesc, 0, len(hostedClusters)),
	}
	for _, hc := range hostedClusters {
		if !hc.DeletionTimestamp.IsZero() {
			continue
		}

		backends, externalDNSBackends, err := getBackendsForHostedCluster(ctx, hc, client)
		if err != nil {
			// don't return an error here, otherwise we block config generation for other HostedClusters and potentially block their KAS access.
			logger.Error(err, "failed to generate router config for hosted cluster", "hosted_cluster", crclient.ObjectKeyFromObject(&hc))
			continue
		}

		p.Backends = append(p.Backends, backends...)
		p.ExternalDNSBackends = append(p.ExternalDNSBackends, externalDNSBackends...)
	}

	out := &bytes.Buffer{}
	if err := routerConfigTemplate.Execute(out, p); err != nil {
		return "", fmt.Errorf("failed to generate router config: %w", err)
	}
	return out.String(), nil
}

func getBackendsForHostedCluster(ctx context.Context, hc hyperv1.HostedCluster, client crclient.Client) ([]backendDesc, []ExternalDNSBackendDesc, error) {
	backends := []backendDesc{}
	externalDNSBackends := []ExternalDNSBackendDesc{}

	var allowedCIDRs string
	if hc.Spec.Networking.APIServer != nil && hc.Spec.Networking.APIServer.AllowedCIDRBlocks != nil {
		allowedCIDRBlocks := make([]string, 0, len(hc.Spec.Networking.APIServer.AllowedCIDRBlocks))
		for _, cidr := range hc.Spec.Networking.APIServer.AllowedCIDRBlocks {
			if cidr != "" {
				allowedCIDRBlocks = append(allowedCIDRBlocks, string(cidr))
			}
		}

		allowedCIDRs = strings.Join(allowedCIDRBlocks, " ")
	}

	hcpNamespace := hc.Namespace + "-" + hc.Name
	kasService := manifests.KubeAPIServerService(hcpNamespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(kasService), kasService); err != nil {
		return nil, nil, fmt.Errorf("failed to get kube-apiserver service: %w", err)
	}

	backends = append(backends, backendDesc{
		Name:         kasService.Namespace + "-" + kasService.Name,
		SVCIP:        kasService.Spec.ClusterIP,
		SVCPort:      kasService.Spec.Ports[0].Port,
		ClusterID:    hc.Spec.ClusterID,
		AllowedCIDRs: allowedCIDRs,
	})

	// This enables traffic from through external DNS.
	routeList := &routev1.RouteList{}
	if err := client.List(ctx, routeList, crclient.InNamespace(hcpNamespace), crclient.HasLabels{util.HCPRouteLabel}); err != nil {
		return nil, nil, fmt.Errorf("failed to list routes: %w", err)
	}

	routes := routeList.Items
	slices.SortFunc(routes, func(a, b routev1.Route) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, route := range routes {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      route.Spec.To.Name,
				Namespace: route.Namespace,
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(svc), svc); err != nil {
			return nil, nil, fmt.Errorf("failed to get service %s: %w", svc.Name, err)
		}

		switch route.Name {
		case manifests.KubeAPIServerExternalPublicRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, ExternalDNSBackendDesc{
				Name:         route.Namespace + "-apiserver",
				HostName:     route.Spec.Host,
				SVCIP:        svc.Spec.ClusterIP,
				SVCPort:      config.KASSVCPort,
				AllowedCIDRs: allowedCIDRs})
		case ignitionserver.Route("").Name:
			externalDNSBackends = append(externalDNSBackends, ExternalDNSBackendDesc{
				Name:     route.Namespace + "-ignition",
				HostName: route.Spec.Host,
				SVCIP:    svc.Spec.ClusterIP,
				SVCPort:  443})
		case manifests.KonnectivityServerRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, ExternalDNSBackendDesc{
				Name:     route.Namespace + "-konnectivity",
				HostName: route.Spec.Host,
				SVCIP:    svc.Spec.ClusterIP,
				SVCPort:  8091})
		case manifests.OauthServerExternalPublicRoute("").Name:
			externalDNSBackends = append(externalDNSBackends, ExternalDNSBackendDesc{
				Name:     route.Namespace + "-oauth",
				HostName: route.Spec.Host,
				SVCIP:    svc.Spec.ClusterIP,
				SVCPort:  6443})
		}
	}

	if hc.Spec.KubeAPIServerDNSName != "" {
		externalDNSBackends = append(externalDNSBackends, ExternalDNSBackendDesc{
			Name:         hcpNamespace + "-apiserver-custom",
			HostName:     hc.Spec.KubeAPIServerDNSName,
			SVCIP:        kasService.Spec.ClusterIP,
			SVCPort:      config.KASSVCPort,
			AllowedCIDRs: allowedCIDRs})
	}

	return backends, externalDNSBackends, nil
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
		c.Image = "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-shared-ingress-main@sha256:d443537f72ec48b2078d24de9852c3369c68c60c06ac82d51c472b2144d41309"
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
	// ServiceExternalTrafficPolicyLocal preserves the client source IP. see: https://kubernetes.io/docs/tasks/access-application-cluster/create-external-load-balancer/#preserving-the-client-source-ip
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
	kasServiceFrontendPort := intstr.FromInt(6443)
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
				{
					Port:     &kasServiceFrontendPort,
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
