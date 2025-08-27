package kas

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// TODO (cewong): Add tests for other params
func TestNewAPIServerParamsAPIAdvertiseAddressAndPort(t *testing.T) {
	tests := []struct {
		apiServiceMapping  hyperv1.ServicePublishingStrategyMapping
		name               string
		advertiseAddress   string
		serviceNetworkCIDR string
		port               *int32
		expectedAddress    string
		expectedPort       int32
	}{
		{
			name:               "not specified",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedPort:       config.KASPodDefaultPort,
		},
		{
			name:               "address specified",
			advertiseAddress:   "1.2.3.4",
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    "1.2.3.4",
			expectedPort:       config.KASPodDefaultPort,
		},
		{
			name:               "port set for default service publishing strategies",
			port:               ptr.To[int32](6789),
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			expectedPort:       6789,
		},
		{
			name: "port set for NodePort service Publishing Strategy",
			apiServiceMapping: hyperv1.ServicePublishingStrategyMapping{
				Service: hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.NodePort,
				},
			},
			port:               ptr.To[int32](6789),
			serviceNetworkCIDR: "10.0.0.0/24",
			expectedAddress:    config.DefaultAdvertiseIPv4Address,
			expectedPort:       6789,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{test.apiServiceMapping}
			hcp.Spec.Networking.ServiceNetwork = append(hcp.Spec.Networking.ServiceNetwork, hyperv1.ServiceNetworkEntry{CIDR: *ipnet.MustParseCIDR(test.serviceNetworkCIDR)})
			hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: test.port, AdvertiseAddress: ptr.To(test.advertiseAddress)}
			p := NewConfigParams(hcp, nil)
			if len(test.advertiseAddress) > 0 {
				g.Expect(test.advertiseAddress).To(Equal(test.expectedAddress))
			}
			g.Expect(p.KASPodPort).To(Equal(test.expectedPort))
		})
	}
}

func createDefaultHostedControlPlane() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.NonePlatform,
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: "",
			},
			Networking: hyperv1.ClusterNetworking{
				ClusterNetwork: []hyperv1.ClusterNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
				},
				ServiceNetwork: []hyperv1.ServiceNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
				},
			},
			ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			DNS: hyperv1.DNSSpec{
				BaseDomain: "example.com",
			},
		},
	}
}

func defaultKubeAPIServerConfigParams() KubeAPIServerConfigParams {
	return KubeAPIServerConfigParams{
		ClusterNetwork:               []string{"10.132.0.0/14"},
		ServiceNetwork:               []string{"172.31.0.0/16"},
		NamedCertificates:            []configv1.APIServerNamedServingCert{},
		KASPodPort:                   6443,
		TLSSecurityProfile:           &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType, Intermediate: &configv1.IntermediateTLSProfile{}},
		AdditionalCORSAllowedOrigins: []string{},
		ExternalRegistryHostNames:    []string{},
		DefaultNodeSelector:          "",
		AdvertiseAddress:             config.DefaultAdvertiseIPv4Address,
		ServiceAccountIssuerURL:      config.DefaultServiceAccountIssuer,
		FeatureGates:                 nil,
		NodePortRange:                config.DefaultServiceNodePortRange,
		ConsolePublicURL:             "https://console-openshift-console.test-hcp.example.com",
		EtcdURL:                      config.DefaultEtcdURL,
		InternalRegistryHostName:     config.DefaultImageRegistryHostname,
		MaxRequestsInflight:          fmt.Sprint(defaultMaxRequestsInflight),
		MaxMutatingRequestsInflight:  fmt.Sprint(defaultMaxMutatingRequestsInflight),
		GoAwayChance:                 fmt.Sprint(defaultGoAwayChance),
		APIServerSTSDirectives:       "max-age=31536000,includeSubDomains,preload",
	}
}

func TestNewConfigParams(t *testing.T) {
	tests := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		featureGates []string
		expected     func(*hyperv1.HostedControlPlane, []string) KubeAPIServerConfigParams
	}{
		{
			name: "defaults",
			hcp:  createDefaultHostedControlPlane(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				return defaultKubeAPIServerConfigParams()
			},
		},
		{
			name:         "with feature gates",
			hcp:          createDefaultHostedControlPlane(),
			featureGates: []string{"SomeFeatureGate=true", "AnotherFeatureGate=false"},
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				return params
			},
		},
		{
			name: "AWS platform",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.Platform.Type = hyperv1.AWSPlatform
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.CloudProvider = "aws"
				return params
			},
		},
		{
			name: "IBM Cloud platform",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.Platform.Type = hyperv1.IBMCloudPlatform
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.APIServerSTSDirectives = "max-age=31536000"
				params.ConsolePublicURL = "https://console-openshift-console.example.com"
				return params
			},
		},
		{
			name: "managed etcd",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.Etcd.ManagementType = hyperv1.Managed
				hcp.Namespace = "test-namespace"
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.EtcdURL = "https://etcd-client.test-namespace.svc:2379"
				return params
			},
		},
		{
			name: "unmanaged etcd",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.Etcd.ManagementType = hyperv1.Unmanaged
				hcp.Spec.Etcd.Unmanaged = &hyperv1.UnmanagedEtcdSpec{
					Endpoint: "https://external-etcd:2379",
				}
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.EtcdURL = "https://external-etcd:2379"
				return params
			},
		},
		{
			name: "single replica controller availability policy",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.GoAwayChance = "0"
				return params
			},
		},
		{
			name: "single replica controller availability policy with custom annotation",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Annotations = map[string]string{
					hyperv1.KubeAPIServerGoAwayChance: "0.002",
				}
				hcp.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.GoAwayChance = "0.002"
				return params
			},
		},
		{
			name: "audit webhook enabled",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.AuditWebhook = &corev1.LocalObjectReference{Name: "audit-webhook"}
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.AuditWebhookEnabled = true
				return params
			},
		},
		{
			name: "with custom annotations",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Annotations = map[string]string{
					hyperv1.KubeAPIServerMaximumRequestsInFlight:         "5000",
					hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight: "2000",
					hyperv1.KubeAPIServerGoAwayChance:                    "0.002",
					hyperv1.DisableProfilingAnnotation:                   manifests.KASDeployment("").Name,
				}
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.FeatureGates = featureGates
				params.DisableProfiling = true
				params.MaxRequestsInflight = "5000"
				params.MaxMutatingRequestsInflight = "2000"
				params.GoAwayChance = "0.002"
				return params
			},
		},
		{
			name: "with full configuration",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := createDefaultHostedControlPlane()
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					APIServer: &configv1.APIServerSpec{
						TLSSecurityProfile: &configv1.TLSSecurityProfile{
							Type:   configv1.TLSProfileModernType,
							Modern: &configv1.ModernTLSProfile{},
						},
						AdditionalCORSAllowedOrigins: []string{"https://example.com"},
					},
					Network: &configv1.NetworkSpec{
						ExternalIP: &configv1.ExternalIPConfig{
							Policy: &configv1.ExternalIPPolicy{
								RejectedCIDRs: []string{"192.168.0.0/16"},
							},
						},
						ServiceNodePortRange: "30000-32767",
					},
					Image: &configv1.ImageSpec{
						ExternalRegistryHostnames: []string{"registry.example.com"},
					},
					Scheduler: &configv1.SchedulerSpec{
						DefaultNodeSelector: "node-role.kubernetes.io/worker=",
					},
					Authentication: &configv1.AuthenticationSpec{
						Type: configv1.AuthenticationTypeIntegratedOAuth,
					},
				}
				hcp.Spec.IssuerURL = "https://custom-issuer.example.com"
				hcp.Spec.Capabilities = &hyperv1.Capabilities{
					Enabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
				}
				return hcp
			}(),
			expected: func(hcp *hyperv1.HostedControlPlane, featureGates []string) KubeAPIServerConfigParams {
				params := defaultKubeAPIServerConfigParams()
				params.ExternalIPConfig = &configv1.ExternalIPConfig{
					Policy: &configv1.ExternalIPPolicy{
						RejectedCIDRs: []string{"192.168.0.0/16"},
					},
				}
				params.NamedCertificates = nil
				params.TLSSecurityProfile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType, Modern: &configv1.ModernTLSProfile{}}
				params.AdditionalCORSAllowedOrigins = []string{"https://example.com"}
				params.ExternalRegistryHostNames = []string{"registry.example.com"}
				params.DefaultNodeSelector = "node-role.kubernetes.io/worker="
				params.ServiceAccountIssuerURL = "https://custom-issuer.example.com"
				params.FeatureGates = featureGates
				params.NodePortRange = "30000-32767"
				params.Authentication = &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeIntegratedOAuth}
				return params
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			actual := NewConfigParams(tc.hcp, tc.featureGates)
			expected := tc.expected(tc.hcp, tc.featureGates)

			g.Expect(actual).To(Equal(expected))
		})
	}
}
