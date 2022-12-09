package nodepool

import (
	"context"
	"fmt"
	"strings"
	"testing"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/testutil"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/vincent-petithory/dataurl"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilpointer "k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func TestAPIServerHAProxyConfig(t *testing.T) {
	image := "ha-proxy-image:latest"
	externalAddress := "cluster.example.com"
	internalAddress := "cluster.internal.example.com"
	config, err := apiServerProxyConfig(image, "", externalAddress, internalAddress, 443, 8443, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	yamlConfig, err := yaml.JSONToYAML(config)
	if err != nil {
		t.Fatalf("cannot convert to yaml: %v", err)
	}
	testutil.CompareWithFixture(t, yamlConfig)
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
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:6443"},
		},
		{
			name: "private cluster uses .local address and custom apiserver port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: utilpointer.Int32Ptr(443)}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "public and private cluster uses .local address",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:6443"},
		},
		{
			name: "public and private cluster uses .local address and custom apiserver port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: utilpointer.Int32Ptr(443)}
			}),

			expectedHAProxyConfigContent: []string{"api." + hc().Name + ".hypershift.local:443"},
		},
		{
			name: "public cluster uses address from kubeconfig",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
			}),
			other: []crclient.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "kk", Namespace: hc().Namespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(kubeconfig(6443)),
				},
			}},

			expectedHAProxyConfigContent: []string{"kubeconfig-host:6443"},
		},
		{
			name: "public cluster uses address from kubeconfig and custom port",
			hc: hc(func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
				hc.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{Port: utilpointer.Int32Ptr(443)}
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "kk"}
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
