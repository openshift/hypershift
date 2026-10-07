package dump

import (
	"context"
	"errors"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	consolelogsazure "github.com/openshift/hypershift/cmd/consolelogs/azure"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/pkg/manifests"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

// VerifyAzureMachineDiagnostics exercises the production collector in a fresh
// artifact directory against an independently captured AzureMachine inventory.
// Unlike teardown collection, missing or empty serial/journal artifacts fail.
func VerifyAzureMachineDiagnostics(ctx context.Context, management client.Client, hc *hyperv1.HostedCluster, credentialsFile, artifactDir, kubeconfigPath string) error {
	if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
		return fmt.Errorf("an Azure HostedCluster is required for diagnostics validation")
	}
	list := &capiazure.AzureMachineList{}
	if err := management.List(ctx, list,
		client.InNamespace(manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)),
		client.MatchingLabels{clusterv1.ClusterNameLabel: hc.Spec.InfraID}); err != nil {
		return err
	}
	var expected []string
	for _, machine := range list.Items {
		if machine.DeletionTimestamp.IsZero() {
			expected = append(expected, machine.Name)
		}
	}
	if len(expected) == 0 {
		return fmt.Errorf("expected at least one Azure worker before diagnostics validation")
	}
	if artifactDir == "" {
		return fmt.Errorf("artifact directory is required for Azure diagnostics validation")
	}
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(artifactDir, "azure-diagnostics-")
	if err != nil {
		return err
	}
	config, err := cmdutil.GetConfigWithKubeconfig(kubeconfigPath)
	if err != nil {
		return err
	}
	report, collectionErr := consolelogsazure.CollectMachineDiagnostics(ctx, management, hc, consolelogsazure.DiagnosticsOptions{
		CredentialsFile: credentialsFile, ArtifactDir: dir, ManagementConfig: config, SSH: consolelogsazure.SSHOptionsFromEnv(),
	}, logr.Discard())
	if err := consolelogsazure.VerifyArtifacts(dir, report, expected, true); err != nil {
		return fmt.Errorf("azure diagnostics artifacts in %s: %w", dir, errors.Join(err, collectionErr))
	}
	return nil
}
