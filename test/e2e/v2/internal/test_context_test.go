//go:build e2ev2

package internal

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
)

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
