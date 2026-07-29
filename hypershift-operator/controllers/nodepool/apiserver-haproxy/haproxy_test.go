package haproxy

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imageapi "github.com/openshift/api/image/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
	"go.uber.org/mock/gomock"
)

func TestAPIServerHAProxyConfig(t *testing.T) {
	image := "ha-proxy-image:latest"
	externalAddress := "cluster.example.com"
	internalAddress := "cluster.internal.example.com"
	serviceNetwork := " 10.134.0.0/16"
	clusterNetwork := " 10.128.0.0/14"

	testCases := []struct {
		name             string
		proxy            string
		noProxy          string
		platform         hyperv1.PlatformType
		useSharedIngress bool
	}{
		{
			name:     "when empty proxy it should create an haproxy",
			proxy:    "",
			platform: "fakePlatform",
			noProxy:  "localhost,127.0.0.1",
		},
		{
			name:     "when noproxy matches internalAddress it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost,127.0.0.1," + internalAddress,
		},
		{
			name:     "when noproxy matches serviceNetwork it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost," + serviceNetwork + ",127.0.0.1,",
		},
		{
			name:     "when noproxy matches clusterNetwork it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost," + clusterNetwork + ",127.0.0.1,",
		},
		{
			name:     "when noproxy matches kubernetes it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost,kubernetes.svc,127.0.0.1,",
		},
		{
			name:     "when noproxy matches the external kas address it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost,127.0.0.1," + externalAddress,
		},
		{
			name:     "when noproxy has leading-dot domain matching external kas address it should create an haproxy",
			proxy:    "proxy",
			platform: "fakePlatform",
			noProxy:  "localhost,.example.com",
		},
		{
			name:             "when use shared router it should use proxy protocol",
			proxy:            "",
			noProxy:          "",
			platform:         "fakePlatform",
			useSharedIngress: true,
		},
		{
			name:     "when IBM cloud platform it should use livez as liveness probe endpoint",
			proxy:    "",
			platform: hyperv1.IBMCloudPlatform,
			noProxy:  "localhost,127.0.0.1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.useSharedIngress {
				azureutil.SetAsAroHCPTest(t)
			}

			config, err := apiServerProxyConfig(image, tc.proxy, "fakeClusterID", externalAddress, internalAddress, 443, 8443,
				tc.proxy, tc.noProxy, serviceNetwork, clusterNetwork, tc.platform, tc.useSharedIngress)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			yamlConfig, err := yaml.JSONToYAML(config)
			if err != nil {
				t.Fatalf("cannot convert to yaml: %v", err)
			}
			testutil.CompareWithFixture(t, yamlConfig)
		})
	}
}

func TestShouldSkipProxyForKAS(t *testing.T) {
	const (
		externalAddress = "api.test.example.com"
		internalAddress = "172.20.0.1"
		serviceNetwork  = "10.134.0.0/16"
		clusterNetwork  = "10.128.0.0/14"
	)

	testCases := []struct {
		name     string
		noProxy  string
		expected bool
	}{
		{
			name:     "When noProxy is empty it should not skip proxy",
			noProxy:  "",
			expected: false,
		},
		{
			name:     "When noProxy contains exact external address it should skip proxy",
			noProxy:  "localhost,127.0.0.1," + externalAddress,
			expected: true,
		},
		{
			name:     "When noProxy contains exact internal address it should skip proxy",
			noProxy:  "localhost,127.0.0.1," + internalAddress,
			expected: true,
		},
		{
			name:     "When noProxy contains leading-dot domain matching external address it should skip proxy",
			noProxy:  "localhost,.example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains bare parent domain matching external address it should skip proxy",
			noProxy:  "localhost,example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains leading-dot partial domain matching external address it should skip proxy",
			noProxy:  "localhost,.test.example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains CIDR covering internal address it should skip proxy",
			noProxy:  "localhost,172.16.0.0/12",
			expected: true,
		},
		{
			name:     "When noProxy contains kubernetes keyword it should skip proxy",
			noProxy:  "localhost,kubernetes.svc,127.0.0.1",
			expected: true,
		},
		{
			name:     "When noProxy contains exact service network CIDR it should skip proxy",
			noProxy:  "localhost," + serviceNetwork,
			expected: true,
		},
		{
			name:     "When noProxy contains exact cluster network CIDR it should skip proxy",
			noProxy:  "localhost," + clusterNetwork,
			expected: true,
		},
		{
			name:     "When noProxy contains wildcard it should skip proxy",
			noProxy:  "*",
			expected: true,
		},
		{
			name:     "When noProxy contains unrelated entries it should not skip proxy",
			noProxy:  "localhost,127.0.0.1,.other-domain.com,192.168.0.0/16",
			expected: false,
		},
		{
			name:     "When noProxy has extra whitespace around entries it should still match",
			noProxy:  " localhost , .example.com , 127.0.0.1 ",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified domain matching KAS port it should skip proxy",
			noProxy:  "localhost,api.test.example.com:6443",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified IP matching KAS port it should skip proxy",
			noProxy:  "localhost,172.20.0.1:6443",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified domain with non-matching port it should not skip proxy",
			noProxy:  "localhost,api.test.example.com:8080",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := shouldSkipProxyForKAS(tc.noProxy, externalAddress, 6443, internalAddress, 6443, serviceNetwork, clusterNetwork)
			if result != tc.expected {
				t.Errorf("shouldSkipProxyForKAS(%q, %q, %q, %q, %q) = %v, want %v",
					tc.noProxy, externalAddress, internalAddress, serviceNetwork, clusterNetwork, result, tc.expected)
			}
		})
	}
}

func TestReconcileHAProxyIgnitionConfig(t *testing.T) {
	hc := func(m ...func(*hyperv1.HostedCluster)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hc",
				Namespace: "clusters",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS:  &hyperv1.AWSPlatformSpec{},
				},
			},
		}
		for _, m := range m {
			m(hc)
		}
		return hc
	}
	const kubeconfigTemplate = `apiVersion: v1
clusters:
- cluster:
    server: https://kubeconfig-host:%d
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`

	kubeconfig := func(port int32) string {
		return fmt.Sprintf(kubeconfigTemplate, port)
	}

	testCases := []struct {
		name                         string
		setupEnv                     func(t *testing.T)
		hc                           *hyperv1.HostedCluster
		other                        []crclient.Object
		expectedHAProxyConfigContent []string
	}{
		{
			name: "private cluster uses .local address",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "private cluster uses .local address and custom apiserver port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: ptr.To[int32](443)}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "private cluster uses .local address and LB kas",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
				hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.LoadBalancer,
						},
					},
				}
			}),
			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:6443"},
		},
		{
			name: "public and private cluster uses .local address",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "public and private cluster uses .local address and custom apiserver port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: ptr.To[int32](443)}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "public and private cluster uses .local address and LB kas",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
				hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.LoadBalancer,
						},
					},
				}
			}),
			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:6443"},
		},
		{
			name: "public cluster uses address from kubeconfig",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),
			other: []crclient.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(kubeconfig(443)),
				},
			}},

			expectedHAProxyConfigContent: []string{"kubeconfig-host:443"},
		},
		{
			name: "public cluster uses address from kubeconfig and custom port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: ptr.To[int32](443)}
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),
			other: []crclient.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(kubeconfig(443)),
				},
			}},

			expectedHAProxyConfigContent: []string{"kubeconfig-host:443"},
		},
		{
			name: "IBMCloud cluster uses livez for liveness probe endpoint",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.Type = hyperv1.IBMCloudPlatform
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: ptr.To[int32](443)}
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
			}),
			other: []crclient.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(kubeconfig(443)),
				},
			}},

			expectedHAProxyConfigContent: []string{"/livez?exclude=etcd&amp;exclude=log"},
		},
		{
			name: "When ARO Swift is enabled it should use .hypershift.local hostname and port 443",
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.Type = hyperv1.AzurePlatform
				hc.Spec.Platform.AWS = nil
				hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					Private: hyperv1.AzurePrivateSpec{
						Type: hyperv1.AzurePrivateTypeSwift,
						Swift: hyperv1.AzureSwiftSpec{
							PodNetworkInstance: "test-swift-instance",
						},
					},
				}
				hc.ObjectMeta.Annotations = map[string]string{
					hyperv1.SwiftPodNetworkInstanceAnnotation: "test-swift-instance",
				}
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
				hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: "api.swift-cluster.example.com",
							},
						},
					},
				}
			}),
			other: []crclient.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(kubeconfig(443)),
				},
			}},
			expectedHAProxyConfigContent: []string{"api.hc.hypershift.local:443"},
		},
		{
			name: "When ARO shared ingress is used it should use the shared ingress LB service IP, port 6443 and proxy protocol",
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.Type = hyperv1.AzurePlatform
				hc.Spec.Platform.AWS = nil
				hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					Topology: hyperv1.AzureTopologyPublic,
					Private: hyperv1.AzurePrivateSpec{
						Type: hyperv1.AzurePrivateTypeSwift,
						Swift: hyperv1.AzureSwiftSpec{
							PodNetworkInstance: "test-pni",
						},
					},
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					},
				}
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
				hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}}
				hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: "api.shared-ingress.example.com",
							},
						},
					},
				}
			}),
			other: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
					Data: map[string][]byte{
						"kubeconfig": []byte(kubeconfig(443)),
					},
				},
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      sharedingress.RouterPublicService().Name,
						Namespace: sharedingress.RouterNamespace,
					},
					Status: corev1.ServiceStatus{
						LoadBalancer: corev1.LoadBalancerStatus{
							Ingress: []corev1.LoadBalancerIngress{
								{IP: "10.0.0.100"},
							},
						},
					},
				},
			},
			expectedHAProxyConfigContent: []string{"10.0.0.100:6443"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}

			r := HAProxy{
				Client:       fake.NewClientBuilder().WithObjects(tc.other...).Build(),
				HAProxyImage: "some-image",
			}
			cfg, err := r.reconcileHAProxyIgnitionConfig(t.Context(),
				tc.hc,
				"cpo-image",
			)
			if err != nil {
				t.Fatalf("reconcileHaProxyIgnitionConfig: %v", err)
			}

			mcfg := &mcfgv1.MachineConfig{}
			if err := yaml.Unmarshal([]byte(cfg), mcfg); err != nil {
				t.Fatalf("cannot unmarshal machine config: %v", err)
			}
			ignitionCfg := &ignitionapi.Config{}
			if err := yaml.Unmarshal(mcfg.Spec.Config.Raw, ignitionCfg); err != nil {
				t.Fatalf("cannot unmarshal ignition config: %v", err)
			}

			var haproxyConfig *dataurl.DataURL
			for _, file := range ignitionCfg.Storage.Files {
				if file.Path == "/etc/kubernetes/apiserver-proxy-config/haproxy.cfg" {
					haproxyConfig, err = dataurl.DecodeString(*file.Contents.Source)
					if err != nil {
						t.Fatalf("cannot decode dataurl: %v", err)
					}
				}
			}
			if haproxyConfig == nil {
				t.Fatalf("Couldn't find haproxy config in ignition config %s", string(mcfg.Spec.Config.Raw))
			}

			for _, line := range tc.expectedHAProxyConfigContent {
				if !strings.Contains(string(haproxyConfig.Data), line) {
					t.Errorf("expected %s in %s", line, string(haproxyConfig.Data))
				}
			}

			testutil.CompareWithFixture(t, haproxyConfig.Data)
		})
	}
}

func testKubeconfig(t *testing.T, server string) []byte {
	t.Helper()
	kc := clientcmdapiv1.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: []clientcmdapiv1.NamedCluster{
			{
				Name: "cluster",
				Cluster: clientcmdapiv1.Cluster{
					Server: server,
				},
			},
		},
		Contexts: []clientcmdapiv1.NamedContext{
			{
				Name: "cluster",
				Context: clientcmdapiv1.Context{
					Cluster:   "cluster",
					Namespace: "default",
				},
			},
		},
		CurrentContext: "cluster",
	}
	data, err := yaml.Marshal(&kc)
	if err != nil {
		t.Fatalf("failed to marshal kubeconfig: %v", err)
	}
	return data
}

func extractProxyPodImage(t *testing.T, rawConfig string) string {
	t.Helper()
	g := NewWithT(t)

	mcfg := &mcfgv1.MachineConfig{}
	g.Expect(yaml.Unmarshal([]byte(rawConfig), mcfg)).To(Succeed())

	ignCfg := &ignitionapi.Config{}
	g.Expect(yaml.Unmarshal(mcfg.Spec.Config.Raw, ignCfg)).To(Succeed())

	for _, file := range ignCfg.Storage.Files {
		if file.Path != "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml" {
			continue
		}
		du, err := dataurl.DecodeString(*file.Contents.Source)
		g.Expect(err).ToNot(HaveOccurred())

		pod := &corev1.Pod{}
		g.Expect(yaml.Unmarshal(du.Data, pod)).To(Succeed())

		for _, c := range pod.Spec.Containers {
			if c.Name == "kubernetes-default-proxy" {
				return c.Image
			}
		}
	}
	return ""
}

func TestReconcileHAProxyIgnitionConfigCPOImage(t *testing.T) {
	testCases := []struct {
		name                      string
		controlPlaneOperatorImage string
	}{
		{
			name:                      "When CPO image is from NodePool release, it should embed that image in the proxy pod",
			controlPlaneOperatorImage: "registry.example.com/np-cpo:4.16",
		},
		{
			name:                      "When CPO image is from HC release, it should embed that image in the proxy pod",
			controlPlaneOperatorImage: "registry.example.com/hc-cpo:4.17",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public},
					},
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPSProxy: "https://proxy.example.com:3128",
						},
					},
				},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "kk"},
				},
			}

			r := HAProxy{
				Client: fake.NewClientBuilder().WithObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc.Namespace},
					Data:       map[string][]byte{"kubeconfig": testKubeconfig(t, "https://kubeconfig-host:6443")},
				}).Build(),
				HAProxyImage: "haproxy:latest",
			}

			cfg, err := r.reconcileHAProxyIgnitionConfig(t.Context(), hc, tc.controlPlaneOperatorImage)
			g.Expect(err).ToNot(HaveOccurred())

			proxyImage := extractProxyPodImage(t, cfg)
			g.Expect(proxyImage).To(Equal(tc.controlPlaneOperatorImage))
		})
	}
}

func TestJoinDefaultPortIfMissing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		addr     string
		expected string
		wantErr  bool
	}{
		{
			name:     "When HTTPS URL has no port it should add port 443",
			addr:     "https://proxy.example.com",
			expected: "https://proxy.example.com:443",
		},
		{
			name:     "When HTTP URL has no port it should add port 80",
			addr:     "http://proxy.example.com",
			expected: "http://proxy.example.com:80",
		},
		{
			name:     "When HTTPS URL already has a port it should keep existing port",
			addr:     "https://proxy.example.com:8443",
			expected: "https://proxy.example.com:8443",
		},
		{
			name:    "When URL has no scheme it should return an error",
			addr:    "proxy.example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := joinDefaultPortIfMissing(tt.addr)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestGenerateHAProxyRawConfigCPOImageSelection(t *testing.T) {
	const (
		hcCPOImage = "registry.example.com/hc-cpo:4.17"
		npCPOImage = "registry.example.com/np-cpo:4.16"
	)

	hcReleaseImage := &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "hypershift",
						From: &corev1.ObjectReference{Name: hcCPOImage},
					},
				},
			},
		},
	}

	imageMetadataWithLabel := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result: &dockerv1client.DockerImageConfig{
			Config: &docker10.DockerConfig{
				Labels: map[string]string{
					ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true",
				},
			},
		},
	}

	testCases := []struct {
		name             string
		nodePoolCPOImage string
		expectedCPOImage string
	}{
		{
			name:             "When NodePoolCPOImage is set, it should use the NodePool CPO image in the proxy pod",
			nodePoolCPOImage: npCPOImage,
			expectedCPOImage: npCPOImage,
		},
		{
			name:             "When NodePoolCPOImage is empty, it should fall back to the HC CPO image in the proxy pod",
			nodePoolCPOImage: "",
			expectedCPOImage: hcCPOImage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)

			mockRelease := NewMockProvider(ctrl)
			mockRelease.EXPECT().
				Lookup(gomock.Any(), "quay.io/openshift-release-dev/ocp-release:4.17.0", gomock.Any()).
				Return(hcReleaseImage, nil)

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Release:    hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.17.0"},
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public},
					},
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPSProxy: "https://proxy.example.com:3128",
						},
					},
				},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"},
				},
			}

			fakeClient := fake.NewClientBuilder().WithObjects(
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters"},
					Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: "clusters"},
					Data:       map[string][]byte{"kubeconfig": testKubeconfig(t, "https://kubeconfig-host:6443")},
				},
			).Build()

			r := HAProxy{
				Client:                  fakeClient,
				HAProxyImage:            "haproxy:latest",
				HypershiftOperatorImage: "hypershift-operator:latest",
				NodePoolCPOImage:        tc.nodePoolCPOImage,
				ReleaseProvider:         mockRelease,
				ImageMetadataProvider:   imageMetadataWithLabel,
			}

			rawConfig, err := r.GenerateHAProxyRawConfig(t.Context(), hc, "clusters-hc")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(rawConfig).ToNot(BeEmpty())

			proxyImage := extractProxyPodImage(t, rawConfig)
			g.Expect(proxyImage).To(Equal(tc.expectedCPOImage))
		})
	}
}
