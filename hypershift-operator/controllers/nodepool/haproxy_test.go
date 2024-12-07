package nodepool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/vincent-petithory/dataurl"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
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
		useSharedIngress bool
	}{
		{
			name:    "when empty proxy it should create an haproxy",
			proxy:   "",
			noProxy: "localhost,127.0.0.1",
		},
		{
			name:    "when noproxy matches internalAddress it should create an haproxy",
			proxy:   "proxy",
			noProxy: "localhost,127.0.0.1," + internalAddress,
		},
		{
			name:    "when noproxy matches serviceNetwork it should create an haproxy",
			proxy:   "proxy",
			noProxy: "localhost," + serviceNetwork + ",127.0.0.1,",
		},
		{
			name:    "when noproxy matches clusterNetwork it should create an haproxy",
			proxy:   "proxy",
			noProxy: "localhost," + clusterNetwork + ",127.0.0.1,",
		},
		{
			name:    "when noproxy matches kubernetes it should create an haproxy",
			proxy:   "proxy",
			noProxy: "localhost,kubernetes.svc,127.0.0.1,",
		},
		{
			name:    "when noproxy matches the external kas address it should create an haproxy",
			proxy:   "proxy",
			noProxy: "localhost,127.0.0.1," + externalAddress,
		},
		{
			name:             "when use shared router it should use proxy protocol",
			proxy:            "",
			noProxy:          "",
			useSharedIngress: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.useSharedIngress {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			}
			config, err := apiServerProxyConfig(image, tc.proxy, "fakeClusterID", externalAddress, internalAddress, 443, 8443,
				tc.proxy, tc.noProxy, serviceNetwork, clusterNetwork)
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			r := &NodePoolReconciler{
				Client: fake.NewClientBuilder().WithObjects(tc.other...).Build(),
			}
			cfg, _, err := r.reconcileHAProxyIgnitionConfig(context.Background(),
				map[string]string{"haproxy-router": "some-image"},
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
