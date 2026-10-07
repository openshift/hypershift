package dump

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDumpAzureMachineDiagnostics(t *testing.T) {
	tests := []struct {
		name           string
		hostedCluster  *hyperv1.HostedCluster
		credentials    string
		collectorError error
		wantCollector  bool
		wantError      string
	}{
		{
			name:          "When the HostedCluster is not Azure, it should skip machine diagnostics",
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AWSPlatform),
		},
		{
			name:          "When Azure credentials are missing, it should still attempt journal collection",
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			wantCollector: true,
		},
		{
			name:          "When an Azure HostedCluster has credentials, it should collect machine diagnostics",
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			credentials:   "/azure/credentials.json",
			wantCollector: true,
		},
		{
			name:           "When Azure diagnostics collection fails, it should return a contextual error",
			hostedCluster:  hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			credentials:    "/azure/credentials.json",
			collectorError: errors.New("Azure API unavailable"),
			wantCollector:  true,
			wantError:      "failed to collect Azure machine diagnostics",
		},
		{
			name:        "When the HostedCluster lookup fails, it should skip diagnostics collection",
			credentials: "/azure/credentials.json",
			wantError:   "failed to get HostedCluster for Azure diagnostics",
		},
	}

	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			objects := []crclient.Object{}
			if test.hostedCluster != nil {
				objects = append(objects, test.hostedCluster)
			}
			managementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			artifactDir := t.TempDir()
			collectorCalled := false

			err := dumpAzureMachineDiagnostics(t.Context(), managementClient, "clusters", "hc", test.credentials, artifactDir, func(ctx context.Context, gotClient crclient.Client, gotHostedCluster *hyperv1.HostedCluster, gotCredentials, gotArtifactDir string) error {
				collectorCalled = true
				deadline, hasDeadline := ctx.Deadline()
				g.Expect(hasDeadline).To(BeTrue(), "diagnostics collection should have a timeout")
				g.Expect(time.Until(deadline)).To(BeNumerically(">", 0))
				g.Expect(time.Until(deadline)).To(BeNumerically("<=", 10*time.Minute))
				g.Expect(gotClient).To(Equal(managementClient))
				g.Expect(gotHostedCluster).To(Equal(test.hostedCluster))
				g.Expect(gotCredentials).To(Equal(test.credentials))
				g.Expect(gotArtifactDir).To(Equal(artifactDir))
				return test.collectorError
			})

			if test.wantError == "" {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.wantError))
			}
			g.Expect(collectorCalled).To(Equal(test.wantCollector))
		})
	}
}

func hostedClusterForDiagnostics(platform hyperv1.PlatformType) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{Type: platform},
		},
	}
}
