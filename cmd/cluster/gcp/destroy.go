package gcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

// NewDestroyCommand creates the GCP cluster destroy command.
func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys a GCP HostedCluster, networking, and IAM",
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&opts.GCPPlatform.PreserveInfra, "preserve-infra", false, "Keep the cluster's networking and IAM resources")
	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			return err
		}
		if err := DestroyCluster(cmd.Context(), opts, client); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			return err
		}
		return nil
	}
	return cmd
}

// DestroyCluster removes the cluster, then its CLI-created networking and IAM.
func DestroyCluster(ctx context.Context, opts *core.DestroyOptions, client crclient.Client) error {
	hc, err := core.GetCluster(ctx, client, opts)
	if err != nil {
		return err
	}
	if hc == nil {
		return fmt.Errorf("HostedCluster %s/%s not found; use destroy infra gcp and destroy iam gcp with the original provisioning parameters for orphan cleanup", opts.Namespace, opts.Name)
	}
	if hc.Spec.Platform.Type != hyperv1.GCPPlatform || hc.Spec.Platform.GCP == nil {
		return fmt.Errorf("HostedCluster %s/%s is not a GCP cluster", opts.Namespace, opts.Name)
	}
	opts.InfraID = hc.Spec.InfraID // Used only for Kubernetes Secret cleanup.
	if opts.GCPPlatform.PreserveInfra {
		// A callback lets core remove the finalizer on retry.
		return core.DestroyCluster(ctx, client, hc, opts, func(context.Context, *core.DestroyOptions, crclient.Client) error {
			return nil
		})
	}

	platform := hc.Spec.Platform.GCP
	infraOpts, iamOpts, err := resolveDestroyOptions(platform)
	if err != nil {
		return fmt.Errorf("cannot resolve GCP cleanup inputs (use --preserve-infra to keep networking and IAM): %w", err)
	}
	if _, err := infraOpts.ValidateNetwork(ctx, opts.Log); err != nil {
		return fmt.Errorf("cannot validate GCP networking (use --preserve-infra to keep networking and IAM): %w", err)
	}
	iamManager, err := gcpinfra.NewIAMManager(ctx, iamOpts.ProjectID, iamOpts.InfraID, "", opts.Log)
	if err != nil {
		return fmt.Errorf("failed to initialize GCP IAM clients: %w", err)
	}
	projectNumber, err := iamManager.GetProjectNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve GCP project number: %w", err)
	}
	if platform.WorkloadIdentity.ProjectNumber != projectNumber {
		return fmt.Errorf("workload identity pool must belong to project %s for automatic cleanup; use --preserve-infra to keep networking and IAM", platform.Project)
	}

	return core.DestroyCluster(ctx, client, hc, opts, func(ctx context.Context, _ *core.DestroyOptions, _ crclient.Client) error {
		var errs []error
		if err := infraOpts.Run(ctx, opts.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy GCP infrastructure: %w", err))
		}
		if err := iamOpts.Run(ctx, opts.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy GCP IAM: %w", err))
		}
		return errors.Join(errs...)
	})
}

// resolveDestroyOptions recovers independent provisioning IDs from CLI resource names.
func resolveDestroyOptions(platform *hyperv1.GCPPlatformSpec) (*gcpinfra.DestroyInfraOptions, *gcpinfra.DestroyIAMOptions, error) {
	networkID, ok := strings.CutSuffix(string(platform.NetworkConfig.Network.Name), "-network")
	if !ok || networkID == "" {
		return nil, nil, fmt.Errorf("network name must follow the CLI convention <infra-id>-network")
	}
	if string(platform.NetworkConfig.PrivateServiceConnectSubnet.Name) != networkID+"-subnet" {
		return nil, nil, fmt.Errorf("PSC subnet name must match the CLI-created subnet %s-subnet", networkID)
	}
	infraOpts := &gcpinfra.DestroyInfraOptions{ProjectID: platform.Project, Region: platform.Region, InfraID: networkID}
	if err := infraOpts.Validate(); err != nil {
		return nil, nil, err
	}

	wif := platform.WorkloadIdentity
	iamID, ok := strings.CutSuffix(wif.PoolID, "-wi-pool")
	if !ok || iamID == "" || wif.ProviderID != iamID+"-k8s-provider" {
		return nil, nil, fmt.Errorf("workload identity pool and provider must follow the CLI naming conventions")
	}
	for _, account := range []struct {
		component string
		email     hyperv1.GCPServiceAccountEmail
	}{
		{"nodepool-mgmt", wif.ServiceAccountsEmails.NodePool},
		{"ctrlplane-op", wif.ServiceAccountsEmails.ControlPlane},
		{"cloud-controller", wif.ServiceAccountsEmails.CloudController},
		{"gcp-pd-csi", wif.ServiceAccountsEmails.Storage},
		{"image-registry", wif.ServiceAccountsEmails.ImageRegistry},
		{"cloud-network", wif.ServiceAccountsEmails.Network},
	} {
		expected := fmt.Sprintf("%s-%s@%s.iam.gserviceaccount.com", iamID, account.component, platform.Project)
		if string(account.email) != expected {
			return nil, nil, fmt.Errorf("service account for %s does not match the CLI naming convention", account.component)
		}
	}
	return infraOpts, &gcpinfra.DestroyIAMOptions{ProjectID: platform.Project, InfraID: iamID}, nil
}
