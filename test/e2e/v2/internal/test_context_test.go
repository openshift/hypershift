//go:build e2ev2

package internal

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
)

func TestSetupTestContextFromEnv(t *testing.T) {
	for _, test := range []struct {
		name, cluster, namespace string
		malformed                bool
	}{
		{name: "When hosted cluster inputs are absent, it should create only a management context"},
		{name: "When hosted cluster inputs are configured, it should derive the control plane namespace", cluster: "example", namespace: "clusters"},
		{name: "When management kubeconfig is malformed, it should return the loading error", malformed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kubeconfig")
			content := "apiVersion: v1\nkind: Config\nclusters:\n- name: management\n  cluster:\n    server: https://127.0.0.1:6443\ncontexts:\n- name: management\n  context:\n    cluster: management\ncurrent-context: management\n"
			if test.malformed {
				content = "clusters: ["
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KUBECONFIG", path)
			t.Setenv("E2E_HOSTED_CLUSTER_NAME", test.cluster)
			t.Setenv("E2E_HOSTED_CLUSTER_NAMESPACE", test.namespace)
			t.Setenv("ARTIFACT_DIR", "/tmp/hypershift-test-artifacts")
			testContext, err := SetupTestContextFromEnv(t.Context())
			if test.malformed {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if testContext.MgmtClient == nil || testContext.ClusterName != test.cluster || testContext.ClusterNamespace != test.namespace || testContext.ArtifactDir != "/tmp/hypershift-test-artifacts" {
				t.Fatal("context inputs were not preserved")
			}
			if test.cluster != "" && testContext.ControlPlaneNamespace != "clusters-example" {
				t.Fatal("incorrect control plane namespace")
			}
		})
	}
}

func TestGetHostedClusterClientViaPortForward(t *testing.T) {
	t.Run("When kube-apiserver has no running pods, it should propagate the error without opening a tunnel", func(t *testing.T) {
		testContext := &TestContext{Context: t.Context(), ControlPlaneNamespace: "clusters-example", MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()}
		client, err := testContext.GetHostedClusterClientViaPortForward(&hyperv1.HostedCluster{})
		if err == nil || client != nil {
			t.Fatal("expected missing kube-apiserver error")
		}
	})
}

func TestGetHostedClusterVersion(t *testing.T) {
	tests := []struct {
		name    string
		status  *hyperv1.ClusterVersionStatus
		want    string
		wantErr string
	}{
		{name: "When version status is nil, it should return an error", wantErr: "has no version in status history"},
		{name: "When version history is empty, it should return an error", status: &hyperv1.ClusterVersionStatus{}, wantErr: "has no version in status history"},
		{name: "When the history version is empty, it should return an error", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{}}}, wantErr: "has no version in status history"},
		{name: "When the version is malformed, it should return an error", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "invalid"}}}, wantErr: "error parsing version"},
		{name: "When the version is stable, it should normalize the patch", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "5.1.3"}}}, want: "5.1.0"},
		{name: "When the version is a nightly, it should normalize the prerelease", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "5.1.0-0.nightly-2026-10-07-120000"}}}, want: "5.1.0"},
		{name: "When the version has build metadata, it should normalize the build", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "5.1.1+build.1"}}}, want: "5.1.0"},
		{name: "When history includes older versions, it should use the newest entry", status: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: "5.1.0"}, {Version: "5.0.2"}}}, want: "5.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Status:     hyperv1.HostedClusterStatus{Version: tt.status},
			}
			tc := &TestContext{
				Context: t.Context(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace,
				MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).Build(),
			}
			version, err := tc.GetHostedClusterVersion()
			if tt.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(version).To(Equal(semver.MustParse(tt.want)))
		})
	}
	t.Run("When the HostedCluster cannot be fetched, it should return an error", func(t *testing.T) {
		tc := &TestContext{
			Context: t.Context(), ClusterName: "missing", ClusterNamespace: "clusters",
			MgmtClient: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(),
		}
		_, err := tc.GetHostedClusterVersion()
		NewWithT(t).Expect(err).To(MatchError(ContainSubstring("failed to get HostedCluster clusters/missing")))
	})
}
