package dump

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	consolelogsazure "github.com/openshift/hypershift/cmd/consolelogs/azure"

	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

// AzureDumpOptions enables Azure worker diagnostics during a cluster dump.
// A nil AzureDumpOptions leaves the generic dump unchanged.
type AzureDumpOptions struct {
	CredentialsFile string
}

type azureDiagnosticsCollector func(context.Context, client.Client, *hyperv1.HostedCluster, consolelogsazure.DiagnosticsOptions, logr.Logger) error

func (opts *DumpOptions) dumpAzureMachineDiagnostics(ctx context.Context, collect azureDiagnosticsCollector) error {
	if opts.Azure == nil || opts.azureDiagnosticsAttempted {
		return nil
	}

	diagnosticsCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	managementClient, err := opts.managementClient()
	if err != nil {
		return fmt.Errorf("failed to get management client for Azure diagnostics: %w", err)
	}
	hostedCluster := &hyperv1.HostedCluster{}
	if err := managementClient.Get(diagnosticsCtx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.Name}, hostedCluster); err != nil {
		return fmt.Errorf("failed to get HostedCluster for Azure diagnostics: %w", err)
	}
	if hostedCluster.Spec.Platform.Type != hyperv1.AzurePlatform {
		opts.azureDiagnosticsAttempted = true
		return nil
	}

	managementConfig, err := opts.managementConfig()
	if err != nil {
		return fmt.Errorf("failed to get management config for Azure diagnostics: %w", err)
	}
	if opts.ImpersonateAs != "" {
		managementConfig = rest.CopyConfig(managementConfig)
		managementConfig.Impersonate = rest.ImpersonationConfig{UserName: opts.ImpersonateAs}
	}
	// DumpClusterWithRetry reuses DumpOptions. Retry setup failures, but run
	// the potentially lengthy collection only once per dump invocation.
	opts.azureDiagnosticsAttempted = true
	if err := collect(diagnosticsCtx, managementClient, hostedCluster, consolelogsazure.DiagnosticsOptions{
		CredentialsFile:  opts.Azure.CredentialsFile,
		ArtifactDir:      opts.ArtifactDir,
		ManagementConfig: managementConfig,
		SSH:              consolelogsazure.SSHOptionsFromEnv(),
	}, opts.Log); err != nil {
		return fmt.Errorf("failed to collect Azure machine diagnostics: %w", err)
	}
	return nil
}

func collectAzureMachineDiagnostics(ctx context.Context, managementClient client.Client, hostedCluster *hyperv1.HostedCluster, opts consolelogsazure.DiagnosticsOptions, logger logr.Logger) error {
	_, err := consolelogsazure.CollectMachineDiagnostics(ctx, managementClient, hostedCluster, opts, logger)
	return err
}
