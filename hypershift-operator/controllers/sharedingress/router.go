package sharedingress

import (
	_ "embed"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	KASSVCLBPort      = 6443
	ExternalDNSLBPort = 443

	// AzurePipIpTagsEnvVar is the environment variable that contains the IP tags for the Azure Public IP.
	// It is used to tag the Azure Public IP associated with the Shared Ingress.
	// Expected format: comma separated key=value pairs with allowed keys: "FirstPartyUsage" or "RoutingPreference".
	// Example: "RoutingPreference=Internet" or "FirstPartyUsage=SomeValue,RoutingPreference=Internet".
	// Both keys and values must be non-empty.
	AzurePipIpTagsEnvVar = "SHARED_INGRESS_AZURE_PIP_IP_TAGS"
)

func hcpRouterLabels() map[string]string {
	return map[string]string{
		"app": "router",
	}
}

const PrivateRouterImage = "haproxy-router"

func ReconcileRouterDeployment(deployment *appsv1.Deployment, hypershiftOperatorImage string) error {
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: ptr.To[int32](2),
		Selector: &metav1.LabelSelector{
			MatchLabels: hcpRouterLabels(),
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: hcpRouterLabels(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					util.BuildContainer(ConfigGeneratorContainer(), buildConfigGeneratorContainer(hypershiftOperatorImage)),
					util.BuildContainer(hcpRouterContainerMain(), buildHCPRouterContainerMain()),
				},
				Volumes: []corev1.Volume{
					{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "runtime-socket",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				ServiceAccountName:           RouterServiceAccount().Name,
				AutomountServiceAccountToken: ptr.To(true),
				Tolerations: []corev1.Toleration{
					{
						Key:      "infra",
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
						Operator: corev1.TolerationOpEqual,
					},
				},
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
							{
								Weight: 100,
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "aro-hcp.azure.com/role",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"infra"},
										},
									},
								},
							},
						},
					},
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
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
		c.Image = images.GetSharedIngressHAProxyImage()
		c.Args = []string{
			"-f", "/usr/local/etc/haproxy",
			"-db",
			"-W",
			"-S", "/var/run/haproxy/admin.sock", // run in master-worker mode and bind to the admin socket to enable hot reloads.
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
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				Name:      "config",
				MountPath: "/usr/local/etc/haproxy",
				ReadOnly:  true,
			}, corev1.VolumeMount{
				Name:      "runtime-socket",
				MountPath: "/var/run/haproxy",
			},
		)
	}
}

func ConfigGeneratorContainer() *corev1.Container {
	return &corev1.Container{
		Name: "config-generator",
	}
}

func buildConfigGeneratorContainer(hypershiftOperatorImage string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Command = []string{
			"/usr/bin/hypershift-operator",
		}
		c.Args = []string{
			"sharedingress-config-generator",
			"--config-path", "/usr/local/etc/haproxy/haproxy.cfg",
			"--haproxy-socket-path", "/var/run/haproxy/admin.sock",
		}
		c.Image = hypershiftOperatorImage

		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				Name:      "config",
				MountPath: "/usr/local/etc/haproxy",
			}, corev1.VolumeMount{
				Name:      "runtime-socket",
				MountPath: "/var/run/haproxy",
			},
		)
	}
}

func ReconcileRouterService(svc *corev1.Service, azurePipIpTags string) error {
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range hcpRouterLabels() {
		svc.Labels[k] = v
	}

	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	if azurePipIpTags != "" {
		svc.Annotations["service.beta.kubernetes.io/azure-pip-ip-tags"] = azurePipIpTags
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
