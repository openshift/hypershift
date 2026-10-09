package gcp

import (
	"context"
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// Test stubs - production uses real implementation, tests can override
var (
	getCluster     = core.GetCluster
	destroyCluster = core.DestroyCluster
	runDestroyIAM  = func(ctx context.Context, opts gcpinfra.DestroyIAMOptions, log logr.Logger) error {
		return opts.Run(ctx, log)
	}
	runDestroyInfra = func(ctx context.Context, opts gcpinfra.DestroyInfraOptions, log logr.Logger) error {
		return opts.Run(ctx, log)
	}
)

// NewDestroyCommand creates a new cobra command for destroying GCP clusters
func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Destroys a GCP HostedCluster and its associated infrastructure",
		SilenceUsage: true,
	}

	opts.GCPPlatform = core.GCPPlatformDestroyOptions{
		PreserveIAM:   false,
		PreserveInfra: false,
	}

	cmd.Flags().BoolVar(&opts.GCPPlatform.PreserveIAM, "preserve-iam", opts.GCPPlatform.PreserveIAM, "Skip deleting IAM resources (Workload Identity Pool, Service Accounts, OIDC Provider). Requires HostedCluster to exist.")
	cmd.Flags().BoolVar(&opts.GCPPlatform.PreserveInfra, "preserve-infra", opts.GCPPlatform.PreserveInfra, "Skip deleting infrastructure (VPC, subnet, router, NAT, firewall). Requires HostedCluster to exist.")

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

// destroyPlatformSpecifics destroys GCP infrastructure and IAM resources.
// Destroy order mirrors reverse of creation: IAM first (identities/permissions), then infrastructure (networking).
// Creation order: infra → IAM → cluster, so destruction is: cluster → IAM → infra.
func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions, _ crclient.Client) error {
	var errs []error
	iamOptions, infraOptions := cleanupOptions(o)

	// Destroy IAM first (unless --preserve-iam is set)
	if !o.GCPPlatform.PreserveIAM {
		o.Log.Info("Destroying IAM")
		if err := runDestroyIAM(ctx, iamOptions, o.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy IAM: %w", err))
		}
	} else {
		o.Log.Info("Skipping IAM destruction (preserve-iam flag set)")
	}

	// Destroy infrastructure last (unless --preserve-infra is set)
	if !o.GCPPlatform.PreserveInfra {
		o.Log.Info("Destroying GCP infrastructure")
		if err := runDestroyInfra(ctx, infraOptions, o.Log); err != nil {
			errs = append(errs, fmt.Errorf("failed to destroy infrastructure: %w", err))
		}
	} else {
		o.Log.Info("Skipping infrastructure destruction (preserve-infra flag set)")
	}

	return errors.Join(errs...)
}

// extractParameters extracts GCP parameters from HostedCluster spec and annotations.
// HostedCluster is the source of truth for GCP resource references and the
// InfraID used for HyperShift-managed cleanup.
func extractParameters(hostedCluster *hyperv1.HostedCluster, o *core.DestroyOptions) error {
	// Preserve the HostedCluster InfraID for HyperShift-managed secret cleanup.
	// GCP resource names are read from the references below, never derived from it.
	o.InfraID = hostedCluster.Spec.InfraID

	// Project, region, and resource names come from the HostedCluster.
	if hostedCluster.Spec.Platform.GCP != nil {
		gcpSpec := hostedCluster.Spec.Platform.GCP
		o.GCPPlatform.ProjectID = gcpSpec.Project
		o.GCPPlatform.Region = gcpSpec.Region
		o.GCPPlatform.NetworkName = string(gcpSpec.NetworkConfig.Network.Name)
		o.GCPPlatform.SubnetName = string(gcpSpec.NetworkConfig.PrivateServiceConnectSubnet.Name)
		o.GCPPlatform.WorkloadIdentityProjectNumber = gcpSpec.WorkloadIdentity.ProjectNumber
		o.GCPPlatform.WorkloadIdentityPoolID = gcpSpec.WorkloadIdentity.PoolID
		o.GCPPlatform.WorkloadIdentityProviderID = gcpSpec.WorkloadIdentity.ProviderID
		o.GCPPlatform.ServiceAccountEmails = gcpSpec.WorkloadIdentity.ServiceAccountsEmails
	}
	// Never use caller-supplied or InfraID-derived names in cluster cleanup.
	o.GCPPlatform.RouterName = ""
	o.GCPPlatform.NATName = ""
	o.GCPPlatform.FirewallRuleName = ""
	if !o.GCPPlatform.PreserveInfra {
		value, ok := hostedCluster.Annotations[infraResourcesAnnotation]
		if !ok {
			return fmt.Errorf("HostedCluster is missing annotation %s; use --preserve-infra and clean up infrastructure separately", infraResourcesAnnotation)
		}
		resources, err := parseInfraResources(value)
		if err != nil {
			return fmt.Errorf("invalid HostedCluster annotation %s: %w; use --preserve-infra to skip infrastructure cleanup", infraResourcesAnnotation, err)
		}
		o.GCPPlatform.RouterName = resources.Router
		o.GCPPlatform.NATName = resources.NAT
		o.GCPPlatform.FirewallRuleName = resources.FirewallRule
	}
	return nil
}

// cleanupOptions maps recorded references to standalone cleanup options. InfraID
// is intentionally omitted: cloud cleanup must never infer resource names from it.
func cleanupOptions(o *core.DestroyOptions) (gcpinfra.DestroyIAMOptions, gcpinfra.DestroyInfraOptions) {
	p := o.GCPPlatform
	iamOptions := gcpinfra.DestroyIAMOptions{
		ProjectID: p.ProjectID, PoolID: p.WorkloadIdentityPoolID, ProviderID: p.WorkloadIdentityProviderID,
		WorkloadIdentityProjectNumber: p.WorkloadIdentityProjectNumber,
		ServiceAccountEmails: map[string]string{
			"nodepool-mgmt":    string(p.ServiceAccountEmails.NodePool),
			"ctrlplane-op":     string(p.ServiceAccountEmails.ControlPlane),
			"cloud-controller": string(p.ServiceAccountEmails.CloudController),
			"gcp-pd-csi":       string(p.ServiceAccountEmails.Storage),
			"image-registry":   string(p.ServiceAccountEmails.ImageRegistry),
			"cloud-network":    string(p.ServiceAccountEmails.Network),
		},
	}
	infraOptions := gcpinfra.DestroyInfraOptions{
		ProjectID: p.ProjectID, Region: p.Region,
		Resources: gcpinfra.NetworkResourceNames{
			Network: p.NetworkName, Subnet: p.SubnetName, Router: p.RouterName,
			NAT: p.NATName, FirewallRule: p.FirewallRuleName,
		},
	}
	return iamOptions, infraOptions
}

// validateInputs checks each requested cleanup stage before cluster deletion.
func validateInputs(o *core.DestroyOptions) error {
	iamOptions, infraOptions := cleanupOptions(o)
	var errs []error
	if (!o.GCPPlatform.PreserveIAM || !o.GCPPlatform.PreserveInfra) && o.GCPPlatform.ProjectID == "" {
		errs = append(errs, fmt.Errorf("project ID is required"))
	}
	if !o.GCPPlatform.PreserveInfra {
		if infraOptions.Region == "" {
			errs = append(errs, fmt.Errorf("HostedCluster GCP region is required when destroying infrastructure"))
		}
		if err := infraOptions.Resources.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid HostedCluster GCP infrastructure references: %w", err))
		}
	}
	if !o.GCPPlatform.PreserveIAM {
		if err := iamOptions.ValidateResourceReferences(); err != nil {
			errs = append(errs, fmt.Errorf("invalid HostedCluster GCP IAM references: %w; use --preserve-iam to skip IAM cleanup", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}
	return nil
}

// DestroyCluster destroys a GCP HostedCluster and its associated infrastructure.
// HostedCluster must exist - it is the source of truth for all destroy parameters.
// For orphaned resource cleanup, use 'hypershift destroy infra gcp' and 'hypershift destroy iam gcp'.
func DestroyCluster(ctx context.Context, o *core.DestroyOptions, client crclient.Client) error {
	hostedCluster, err := getCluster(ctx, client, o)
	if err != nil {
		return err
	}

	if hostedCluster == nil {
		return fmt.Errorf("HostedCluster %s/%s not found. Cannot destroy infrastructure without cluster as source of truth.\n\nTo clean up orphaned GCP resources manually:\n  hypershift destroy infra gcp --infra-id=<id> --project-id=<project> --region=<region>\n  hypershift destroy iam gcp --infra-id=<id> --project-id=<project>", o.Namespace, o.Name)
	}

	if err := extractParameters(hostedCluster, o); err != nil {
		return err
	}

	if err := validateInputs(o); err != nil {
		return err
	}

	return destroyCluster(ctx, client, hostedCluster, o, destroyPlatformSpecifics)
}
