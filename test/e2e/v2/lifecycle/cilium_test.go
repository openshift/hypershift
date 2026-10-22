//go:build e2ev2

package lifecycle

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	ciliumassets "github.com/openshift/hypershift/test/e2e/assets/cilium"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestInstallCilium(t *testing.T) {
	for _, test := range []struct {
		name        string
		replicas    int32
		networkType hyperv1.NetworkType
		kubeconfig  *corev1.LocalObjectReference
	}{
		{name: "When no workers are configured, it should fail before installing assets", networkType: hyperv1.Other},
		{name: "When worker count is negative, it should fail before installing assets", replicas: -1, networkType: hyperv1.Other},
		{name: "When another CNI is configured, it should reject the fixture", replicas: 1, networkType: hyperv1.OVNKubernetes},
		{name: "When hosted kubeconfig is absent, it should fail instead of using management credentials", replicas: 1, networkType: hyperv1.Other},
		{name: "When the referenced kubeconfig secret is absent, it should propagate the lookup error", replicas: 1, networkType: hyperv1.Other, kubeconfig: &corev1.LocalObjectReference{Name: "missing"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cilium", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{NetworkType: test.networkType}}, Status: hyperv1.HostedClusterStatus{KubeConfig: test.kubeconfig}}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
			if err := installCilium(t.Context(), client, cluster, test.replicas); err == nil {
				t.Fatal("expected prerequisite error")
			}
		})
	}
	t.Run("When embedded operator assets are used, it should include all thirteen setup manifests", func(t *testing.T) {
		entries, err := ciliumassets.Manifests.ReadDir("v1.15.1")
		if err != nil || len(entries) != 13 {
			t.Fatalf("expected the complete baseline operator asset set: %v, %d files", err, len(entries))
		}
	})
}

func TestApplyCiliumObject(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "When a typed fixture is applied, it should retain its kind and selected namespace"
		if fail {
			name = "When server-side apply fails, it should preserve the API error"
		}
		t.Run(name, func(t *testing.T) {
			expected := errors.New("apply rejected")
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(_ context.Context, _ crclient.WithWatch, object crclient.Object, patch crclient.Patch, _ ...crclient.PatchOption) error {
					if object.GetName() != "cilium" || object.GetObjectKind().GroupVersionKind().Kind != "Namespace" || patch.Type() != "application/apply-patch+yaml" {
						t.Fatal("fixture kind or apply mode was lost")
					}
					if fail {
						return expected
					}
					return nil
				},
			}).Build()
			err := applyCiliumObject(t.Context(), client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "cilium"}})
			if fail && !errors.Is(err, expected) || !fail && err != nil {
				t.Fatalf("unexpected fixture apply result: %v", err)
			}
		})
	}
}

func TestBuildCiliumConfig(t *testing.T) {
	for _, test := range []struct {
		name      string
		networks  []configv1.ClusterNetworkEntry
		wantCIDR  string
		wantMask  int64
		wantError bool
	}{
		{name: "When network status is not initialized, it should preserve the baseline IPv4 defaults", wantCIDR: "10.132.0.0/14", wantMask: 23},
		{name: "When IPv4 network is configured, it should use the actual CIDR and host prefix", networks: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14", HostPrefix: 24}}, wantCIDR: "10.128.0.0/14", wantMask: 24},
		{name: "When IPv6 precedes IPv4, it should select the IPv4 network", networks: []configv1.ClusterNetworkEntry{{CIDR: "fd00::/48", HostPrefix: 64}, {CIDR: "10.128.0.0/14", HostPrefix: 23}}, wantCIDR: "10.128.0.0/14", wantMask: 23},
		{name: "When host prefix is absent, it should preserve the baseline default", networks: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}}, wantCIDR: "10.128.0.0/14", wantMask: 23},
		{name: "When no usable IPv4 network exists, it should report the unsupported configuration", networks: []configv1.ClusterNetworkEntry{{CIDR: "invalid"}}, wantError: true},
		{name: "When host prefix is smaller than the network mask, it should reject invalid IPAM", networks: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/16", HostPrefix: 14}}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := buildCiliumConfig(test.networks)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected config result: %v", err)
			}
			if test.wantError {
				return
			}
			cidrs, _, _ := unstructured.NestedStringSlice(config.Object, "spec", "ipam", "operator", "clusterPoolIPv4PodCIDRList")
			mask, _, _ := unstructured.NestedInt64(config.Object, "spec", "ipam", "operator", "clusterPoolIPv4MaskSize")
			if len(cidrs) != 1 || cidrs[0] != test.wantCIDR || mask != test.wantMask {
				t.Fatal("Cilium IPAM does not match the actual network")
			}
		})
	}
}

func TestCiliumDaemonSetReady(t *testing.T) {
	for _, test := range []struct {
		name      string
		observed  int64
		desired   int32
		ready     int32
		updated   int32
		wantReady bool
	}{
		{name: "When every expected agent is ready and updated, it should accept the rollout", observed: 2, desired: 2, ready: 2, updated: 2, wantReady: true},
		{name: "When status has not observed the current generation, it should keep waiting", observed: 1, desired: 2, ready: 2, updated: 2},
		{name: "When the daemonset has no scheduled agents, it should reject a vacuous rollout", observed: 2},
		{name: "When some agents are not ready, it should keep waiting", observed: 2, desired: 2, ready: 1, updated: 2},
		{name: "When old agents remain, it should keep waiting", observed: 2, desired: 2, ready: 2, updated: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemonSet := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Status: appsv1.DaemonSetStatus{ObservedGeneration: test.observed, DesiredNumberScheduled: test.desired, NumberReady: test.ready, UpdatedNumberScheduled: test.updated}}
			if ciliumDaemonSetReady(daemonSet, 2) != test.wantReady {
				t.Fatal("unexpected Cilium agent readiness")
			}
		})
	}
}
