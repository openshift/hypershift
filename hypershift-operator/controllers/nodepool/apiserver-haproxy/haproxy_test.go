package haproxy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/pullsecret"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
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
			name:     "When noProxy is empty, it should not skip proxy",
			noProxy:  "",
			expected: false,
		},
		{
			name:     "When noProxy contains exact external address, it should skip proxy",
			noProxy:  "localhost,127.0.0.1," + externalAddress,
			expected: true,
		},
		{
			name:     "When noProxy contains exact internal address, it should skip proxy",
			noProxy:  "localhost,127.0.0.1," + internalAddress,
			expected: true,
		},
		{
			name:     "When noProxy contains leading-dot domain matching external address, it should skip proxy",
			noProxy:  "localhost,.example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains bare parent domain matching external address, it should skip proxy",
			noProxy:  "localhost,example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains leading-dot partial domain matching external address, it should skip proxy",
			noProxy:  "localhost,.test.example.com",
			expected: true,
		},
		{
			name:     "When noProxy contains CIDR covering internal address, it should skip proxy",
			noProxy:  "localhost,172.16.0.0/12",
			expected: true,
		},
		{
			name:     "When noProxy contains kubernetes keyword, it should skip proxy",
			noProxy:  "localhost,kubernetes.svc,127.0.0.1",
			expected: true,
		},
		{
			name:     "When noProxy contains exact service network CIDR, it should skip proxy",
			noProxy:  "localhost," + serviceNetwork,
			expected: true,
		},
		{
			name:     "When noProxy contains exact cluster network CIDR, it should skip proxy",
			noProxy:  "localhost," + clusterNetwork,
			expected: true,
		},
		{
			name:     "When noProxy contains wildcard, it should skip proxy",
			noProxy:  "*",
			expected: true,
		},
		{
			name:     "When noProxy contains unrelated entries, it should not skip proxy",
			noProxy:  "localhost,127.0.0.1,.other-domain.com,192.168.0.0/16",
			expected: false,
		},
		{
			name:     "When noProxy has extra whitespace around entries, it should still match",
			noProxy:  " localhost , .example.com , 127.0.0.1 ",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified domain matching KAS port, it should skip proxy",
			noProxy:  "localhost,api.test.example.com:6443",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified IP matching KAS port, it should skip proxy",
			noProxy:  "localhost,172.20.0.1:6443",
			expected: true,
		},
		{
			name:     "When noProxy contains port-qualified domain with non-matching port, it should not skip proxy",
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

func TestHAProxy_StaticPodCPOImage(t *testing.T) {
	const (
		hostedClusterCPOImage = "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		nodePoolCPOImage      = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:2222222222222222222222222222222222222222222222222222222222222222"
		operatorImage         = "quay.io/hypershift/hypershift-operator:latest"
		proxyPodPath          = "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml"
	)

	testCases := []struct {
		name                string
		controlPlaneRelease *hyperv1.Release
		nodePoolCPOImage    string
		expectedImage       string
	}{
		{
			name:             "When no control plane release is pinned, it should use the HostedCluster CPO image",
			nodePoolCPOImage: nodePoolCPOImage,
			expectedImage:    hostedClusterCPOImage,
		},
		{
			name:                "When a control plane release is pinned, it should use the NodePool release CPO image",
			controlPlaneRelease: &hyperv1.Release{Image: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/ocp-release:4.21.10-x86_64"},
			nodePoolCPOImage:    nodePoolCPOImage,
			expectedImage:       nodePoolCPOImage,
		},
		{
			name:                "When a control plane release is pinned and the NodePool release has no CPO image, it should use the HostedCluster CPO image",
			controlPlaneRelease: &hyperv1.Release{Image: "quay.io/redhat-user-workloads/crt-redhat-acm-tenant/ocp-release:4.21.10-x86_64"},
			expectedImage:       hostedClusterCPOImage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					ControlPlaneRelease: tc.controlPlaneRelease,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public},
					},
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
					// The kubernetes-default-proxy static pod is only rendered when
					// the nodes reach the control plane through an HTTPS proxy.
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{HTTPSProxy: "http://proxy.example.com:3128"},
					},
				},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "kk"},
				},
			}
			kubeconfig := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hcluster.Namespace},
				Data: map[string][]byte{"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://kubeconfig-host:443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`)},
			}

			r := HAProxy{
				Client:                  fake.NewClientBuilder().WithObjects(kubeconfig).Build(),
				HAProxyImage:            "some-image",
				HypershiftOperatorImage: operatorImage,
				NodePoolCPOImage:        tc.nodePoolCPOImage,
			}
			cfg, err := r.reconcileHAProxyIgnitionConfig(t.Context(), hcluster, hostedClusterCPOImage)
			g.Expect(err).ToNot(HaveOccurred())

			mcfg := &mcfgv1.MachineConfig{}
			g.Expect(yaml.Unmarshal([]byte(cfg), mcfg)).To(Succeed())
			ignitionCfg := &ignitionapi.Config{}
			g.Expect(yaml.Unmarshal(mcfg.Spec.Config.Raw, ignitionCfg)).To(Succeed())

			var proxyPod *corev1.Pod
			for _, file := range ignitionCfg.Storage.Files {
				if file.Path != proxyPodPath {
					continue
				}
				data, err := dataurl.DecodeString(*file.Contents.Source)
				g.Expect(err).ToNot(HaveOccurred())
				proxyPod = &corev1.Pod{}
				g.Expect(yaml.Unmarshal(data.Data, proxyPod)).To(Succeed())
			}
			g.Expect(proxyPod).ToNot(BeNil(), "the kubernetes-default-proxy static pod should be rendered")
			g.Expect(proxyPod.Spec.Containers).To(HaveLen(1))
			g.Expect(proxyPod.Spec.Containers[0].Command[:2]).To(Equal([]string{"control-plane-operator", "kubernetes-default-proxy"}))
			g.Expect(proxyPod.Spec.Containers[0].Image).To(Equal(tc.expectedImage))
			// The hypershift-operator image does not ship control-plane-operator.
			g.Expect(proxyPod.Spec.Containers[0].Image).ToNot(Equal(operatorImage))
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
			name:     "When HTTPS URL has no port, it should add port 443",
			addr:     "https://proxy.example.com",
			expected: "https://proxy.example.com:443",
		},
		{
			name:     "When HTTP URL has no port, it should add port 80",
			addr:     "http://proxy.example.com",
			expected: "http://proxy.example.com:80",
		},
		{
			name:     "When HTTPS URL already has a port, it should keep existing port",
			addr:     "https://proxy.example.com:8443",
			expected: "https://proxy.example.com:8443",
		},
		{
			name:    "When URL has no scheme, it should return an error",
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

func TestResolveHAProxyImage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		componentImages map[string]string
		expectedImage   string
		wantErr         bool
	}{
		{
			name: "When only the preferred image name is present, it should use it",
			componentImages: map[string]string{
				HAProxyImageNames[0]: "registry.test.io/openshift/haproxy-router-haproxy32:latest",
			},
			expectedImage: "registry.test.io/openshift/haproxy-router-haproxy32:latest",
		},
		{
			name: "When only a fallback image name is present, it should use it",
			componentImages: map[string]string{
				HAProxyImageNames[1]: "registry.test.io/openshift/haproxy-router:latest",
			},
			expectedImage: "registry.test.io/openshift/haproxy-router:latest",
		},
		{
			name: "When both the preferred and fallback image names are present, it should prefer the first one",
			componentImages: map[string]string{
				HAProxyImageNames[0]: "registry.test.io/openshift/haproxy-router-haproxy32:latest",
				HAProxyImageNames[1]: "registry.test.io/openshift/haproxy-router:latest",
			},
			expectedImage: "registry.test.io/openshift/haproxy-router-haproxy32:latest",
		},
		{
			name:            "When none of the known image names are present, it should return an error",
			componentImages: map[string]string{"some-other-image": "registry.test.io/openshift/some-other-image:latest"},
			wantErr:         true,
		},
		{
			name:            "When componentImages is empty, it should return an error",
			componentImages: map[string]string{},
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := ResolveHAProxyImage(tt.componentImages)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expectedImage))
		})
	}
}

// recordingImageMetadataProvider records the pull secret each ImageMetadata call
// received and reports the control plane operator image as skipping HAProxy.
type recordingImageMetadataProvider struct {
	imageregistry.ImageMetadataProvider
	pullSecrets [][]byte
}

func (p *recordingImageMetadataProvider) ImageMetadata(_ context.Context, _ string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	p.pullSecrets = append(p.pullSecrets, pullSecret)
	return &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
		Labels: map[string]string{ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true"},
	}}, nil
}

// releaseProviderStub returns a release payload that ships a control plane
// operator image.
type releaseProviderStub struct {
	releaseinfo.Provider
	cpoImage string
}

func (p *releaseProviderStub) Lookup(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: "4.21.10"},
		Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{
			{Name: "hypershift", From: &corev1.ObjectReference{Name: p.cpoImage}},
		}},
	}}, nil
}

func TestHAProxy_IsHAProxyIgnitionConfigManaged(t *testing.T) {
	const (
		controlPlaneNamespace = "clusters-example"
		providerHost          = "quay.io/openshift-release-dev/ocp-release-private"
		payloadCPOImage       = providerHost + "@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		annotatedCPOImage     = "quay.io/hypershift/hypershift:latest"
	)
	clusterPullSecret := []byte(`{"auths":{"registry.redhat.io":{"auth":"dXNlcjpwYXNz"}}}`)
	providerPullSecret := []byte(`{"auths":{"` + providerHost + `":{"auth":"cHJvdmlkZXI6cGFzcw=="}}}`)

	pinned := func(annotations map[string]string) *hyperv1.HostedCluster {
		return &hyperv1.HostedCluster{
			// AutoNode passes a synthetic HostedCluster whose namespace already is
			// the control plane namespace, so it must not be derived from it again.
			ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: controlPlaneNamespace, Annotations: annotations},
			Spec: hyperv1.HostedClusterSpec{
				PullSecret:          corev1.LocalObjectReference{Name: "pull-secret"},
				ControlPlaneRelease: &hyperv1.Release{Image: providerHost + ":4.21.10-x86_64"},
			},
		}
	}

	tests := []struct {
		name               string
		hostedCluster      *hyperv1.HostedCluster
		expectedCPOImage   string
		expectProviderAuth bool
	}{
		{
			name:               "When the CPO image comes from a pinned control plane release, it should read its metadata with the synced copy from the given namespace",
			hostedCluster:      pinned(nil),
			expectedCPOImage:   payloadCPOImage,
			expectProviderAuth: true,
		},
		{
			name:             "When the CPO image comes from the annotation, it should read its metadata with the cluster pull secret",
			hostedCluster:    pinned(map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: annotatedCPOImage}),
			expectedCPOImage: annotatedCPOImage,
		},
		{
			name: "When the HostedCluster pins no control plane release, it should use the cluster pull secret unchanged",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: controlPlaneNamespace},
				Spec:       hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "pull-secret"}},
			},
			expectedCPOImage: payloadCPOImage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithObjects(
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: controlPlaneNamespace},
					Data:       map[string][]byte{corev1.DockerConfigJsonKey: clusterPullSecret},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pullsecret.SyncedControlPlaneReleaseSecretName,
						Namespace: controlPlaneNamespace,
						Labels:    map[string]string{pullsecret.ManagedByLabel: "true"},
					},
					Data: map[string][]byte{corev1.DockerConfigJsonKey: providerPullSecret},
				},
			).Build()
			metadataProvider := &recordingImageMetadataProvider{}
			r := &HAProxy{
				Client:                  client,
				HypershiftOperatorImage: "quay.io/hypershift/hypershift-operator:latest",
				ReleaseProvider:         &releaseProviderStub{cpoImage: payloadCPOImage},
				ImageMetadataProvider:   metadataProvider,
			}

			managed, cpoImage, err := r.isHAProxyIgnitionConfigManaged(t.Context(), tc.hostedCluster, controlPlaneNamespace)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(managed).To(BeTrue())
			g.Expect(cpoImage).To(Equal(tc.expectedCPOImage))
			g.Expect(metadataProvider.pullSecrets).To(HaveLen(1))
			if tc.expectProviderAuth {
				g.Expect(string(metadataProvider.pullSecrets[0])).To(ContainSubstring(providerHost))
			} else {
				g.Expect(metadataProvider.pullSecrets[0]).To(Equal(clusterPullSecret))
			}
		})
	}
}
