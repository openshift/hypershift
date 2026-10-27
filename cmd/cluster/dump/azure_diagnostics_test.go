package dump

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	consolelogsazure "github.com/openshift/hypershift/cmd/consolelogs/azure"
	"github.com/openshift/hypershift/cmd/util"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestDumpAzureMachineDiagnostics(t *testing.T) {
	tests := []struct {
		name           string
		azure          *AzureDumpOptions
		hostedCluster  *hyperv1.HostedCluster
		collectorError error
		wantCollector  bool
		wantError      string
	}{
		{
			name:          "When Azure dump options are absent, it should skip machine diagnostics",
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AzurePlatform),
		},
		{
			name:          "When the HostedCluster is not Azure, it should skip machine diagnostics",
			azure:         &AzureDumpOptions{CredentialsFile: "/azure/credentials.json"},
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AWSPlatform),
		},
		{
			name:          "When Azure credentials are missing, it should still attempt journal collection",
			azure:         &AzureDumpOptions{},
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			wantCollector: true,
		},
		{
			name:          "When an Azure HostedCluster has credentials, it should collect machine diagnostics",
			azure:         &AzureDumpOptions{CredentialsFile: "/azure/credentials.json"},
			hostedCluster: hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			wantCollector: true,
		},
		{
			name:           "When Azure diagnostics collection fails, it should return a contextual error",
			azure:          &AzureDumpOptions{CredentialsFile: "/azure/credentials.json"},
			hostedCluster:  hostedClusterForDiagnostics(hyperv1.AzurePlatform),
			collectorError: errors.New("Azure API unavailable"),
			wantCollector:  true,
			wantError:      "failed to collect Azure machine diagnostics",
		},
		{
			name:      "When the HostedCluster lookup fails, it should skip diagnostics collection",
			azure:     &AzureDumpOptions{CredentialsFile: "/azure/credentials.json"},
			wantError: "failed to get HostedCluster for Azure diagnostics",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			objects := []client.Object{}
			if test.hostedCluster != nil {
				objects = append(objects, test.hostedCluster)
			}
			managementClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).Build()
			managementConfig := &rest.Config{Host: "https://management.example"}
			artifactDir := t.TempDir()
			opts := &DumpOptions{
				Namespace:   "clusters",
				Name:        "hc",
				ArtifactDir: artifactDir,
				Azure:       test.azure,
				Log:         logr.Discard(),
				Client:      managementClient,
				ClientProvider: &util.ClientProvider{Config: func(string) (*rest.Config, error) {
					return managementConfig, nil
				}},
			}
			collectorCalled := false

			err := opts.dumpAzureMachineDiagnostics(t.Context(), func(ctx context.Context, gotClient client.Client, gotHostedCluster *hyperv1.HostedCluster, gotOptions consolelogsazure.DiagnosticsOptions, gotLog logr.Logger) error {
				collectorCalled = true
				deadline, hasDeadline := ctx.Deadline()
				g.Expect(hasDeadline).To(BeTrue(), "diagnostics collection should have a timeout")
				g.Expect(time.Until(deadline)).To(BeNumerically(">", 0))
				g.Expect(time.Until(deadline)).To(BeNumerically("<=", 10*time.Minute))
				g.Expect(gotClient).To(BeIdenticalTo(managementClient))
				g.Expect(gotHostedCluster).To(Equal(test.hostedCluster))
				g.Expect(gotOptions.CredentialsFile).To(Equal(test.azure.CredentialsFile))
				g.Expect(gotOptions.ArtifactDir).To(Equal(artifactDir))
				g.Expect(gotOptions.ManagementConfig).To(BeIdenticalTo(managementConfig))
				return test.collectorError
			})

			if test.wantError == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.wantError))
			}
			g.Expect(collectorCalled).To(Equal(test.wantCollector))
		})
	}

	t.Run("When the dump retries after collecting diagnostics, it should not collect them again", func(t *testing.T) {
		g := NewWithT(t)
		hostedCluster := hostedClusterForDiagnostics(hyperv1.AzurePlatform)
		managementClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hostedCluster).Build()
		opts := &DumpOptions{
			Namespace: "clusters",
			Name:      "hc",
			Azure:     &AzureDumpOptions{},
			Client:    managementClient,
			ClientProvider: &util.ClientProvider{Config: func(string) (*rest.Config, error) {
				return &rest.Config{}, nil
			}},
		}
		calls := 0
		collect := func(context.Context, client.Client, *hyperv1.HostedCluster, consolelogsazure.DiagnosticsOptions, logr.Logger) error {
			calls++
			return nil
		}

		g.Expect(opts.dumpAzureMachineDiagnostics(t.Context(), collect)).To(Succeed())
		g.Expect(opts.dumpAzureMachineDiagnostics(t.Context(), collect)).To(Succeed())
		g.Expect(calls).To(Equal(1))
	})

	t.Run("When HostedCluster lookup recovers, it should collect diagnostics on the next attempt", func(t *testing.T) {
		g := NewWithT(t)
		managementClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		opts := &DumpOptions{
			Namespace: "clusters",
			Name:      "hc",
			Azure:     &AzureDumpOptions{},
			Client:    managementClient,
			ClientProvider: &util.ClientProvider{Config: func(string) (*rest.Config, error) {
				return &rest.Config{}, nil
			}},
		}
		calls := 0
		collect := func(context.Context, client.Client, *hyperv1.HostedCluster, consolelogsazure.DiagnosticsOptions, logr.Logger) error {
			calls++
			return nil
		}

		g.Expect(opts.dumpAzureMachineDiagnostics(t.Context(), collect)).To(HaveOccurred())
		g.Expect(managementClient.Create(t.Context(), hostedClusterForDiagnostics(hyperv1.AzurePlatform))).To(Succeed())
		g.Expect(opts.dumpAzureMachineDiagnostics(t.Context(), collect)).To(Succeed())
		g.Expect(calls).To(Equal(1))
	})

	t.Run("When the dump impersonates a user, it should use that client and REST config", func(t *testing.T) {
		g := NewWithT(t)
		hostedCluster := hostedClusterForDiagnostics(hyperv1.AzurePlatform)
		impersonatedClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hostedCluster).Build()
		baseConfig := &rest.Config{Host: "https://management.example"}
		opts := &DumpOptions{
			Namespace:     "clusters",
			Name:          "hc",
			Azure:         &AzureDumpOptions{},
			ImpersonateAs: "test-user",
			ClientProvider: &util.ClientProvider{
				ImpersonatedClient: func(_ string, user string) (client.Client, error) {
					g.Expect(user).To(Equal("test-user"))
					return impersonatedClient, nil
				},
				Config: func(string) (*rest.Config, error) { return baseConfig, nil },
			},
		}
		called := false
		err := opts.dumpAzureMachineDiagnostics(t.Context(), func(_ context.Context, gotClient client.Client, _ *hyperv1.HostedCluster, gotOptions consolelogsazure.DiagnosticsOptions, _ logr.Logger) error {
			called = true
			g.Expect(gotClient).To(BeIdenticalTo(impersonatedClient))
			g.Expect(gotOptions.ManagementConfig.Impersonate.UserName).To(Equal("test-user"))
			g.Expect(baseConfig.Impersonate.UserName).To(BeEmpty(), "the provider config should remain unchanged")
			return nil
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(called).To(BeTrue())
	})
}

func hostedClusterForDiagnostics(platform hyperv1.PlatformType) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "hc", Namespace: "clusters"},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{Type: platform},
		},
	}
}
