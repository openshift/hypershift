package gcp

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var _ core.Platform = (*CreateOptions)(nil)

const (
	flagProject                       = "project"
	flagRegion                        = "region"
	flagNetwork                       = "network"
	flagPrivateServiceConnectSubnet   = "private-service-connect-subnet"
	flagWorkloadIdentityProjectNumber = "workload-identity-project-number"
	flagWorkloadIdentityPoolID        = "workload-identity-pool-id"
	flagWorkloadIdentityProviderID    = "workload-identity-provider-id"
	flagNodePoolServiceAccount        = "node-pool-service-account"
	flagControlPlaneServiceAccount    = "control-plane-service-account"
	flagCloudControllerServiceAccount = "cloud-controller-service-account"
)

// RawCreateOptions contains the raw command-line options for creating a GCP cluster
type RawCreateOptions struct {
	// Project is the GCP project ID where the HostedCluster will be created
	Project string

	// Region is the GCP region where the HostedCluster will be created
	Region string

	// Network is the VPC network name for the cluster
	Network string

	// PrivateServiceConnectSubnet is the subnet for Private Service Connect endpoints
	PrivateServiceConnectSubnet string

	// WorkloadIdentityProjectNumber is the numeric GCP project identifier for WIF configuration
	WorkloadIdentityProjectNumber string

	// WorkloadIdentityPoolID is the workload identity pool identifier
	WorkloadIdentityPoolID string

	// WorkloadIdentityProviderID is the workload identity provider identifier
	WorkloadIdentityProviderID string

	// NodePoolServiceAccount is the Google Service Account email for CAPG controllers
	NodePoolServiceAccount string

	// ControlPlaneServiceAccount is the Google Service Account email for the Control Plane Operator
	ControlPlaneServiceAccount string

	// CloudControllerServiceAccount is the Google Service Account email for the Cloud Controller Manager
	CloudControllerServiceAccount string
}

// BindOptions binds the GCP-specific flags to the provided flag set
func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Project, flagProject, opts.Project, "GCP project ID where the HostedCluster will be created")
	flags.StringVar(&opts.Region, flagRegion, opts.Region, "GCP region where the HostedCluster will be created")
	flags.StringVar(&opts.Network, flagNetwork, opts.Network, "VPC network name for the cluster")
	flags.StringVar(&opts.PrivateServiceConnectSubnet, flagPrivateServiceConnectSubnet, opts.PrivateServiceConnectSubnet, "Subnet for Private Service Connect endpoints")
	flags.StringVar(&opts.WorkloadIdentityProjectNumber, flagWorkloadIdentityProjectNumber, opts.WorkloadIdentityProjectNumber, "Numeric GCP project identifier for Workload Identity Federation (from `hypershift infra create gcp` output)")
	flags.StringVar(&opts.WorkloadIdentityPoolID, flagWorkloadIdentityPoolID, opts.WorkloadIdentityPoolID, "Workload Identity Pool ID (from `hypershift infra create gcp` output)")
	flags.StringVar(&opts.WorkloadIdentityProviderID, flagWorkloadIdentityProviderID, opts.WorkloadIdentityProviderID, "Workload Identity Provider ID (from `hypershift infra create gcp` output)")
	flags.StringVar(&opts.NodePoolServiceAccount, flagNodePoolServiceAccount, opts.NodePoolServiceAccount, "Google Service Account email for NodePool CAPG controllers (from `hypershift infra create gcp` output)")
	flags.StringVar(&opts.ControlPlaneServiceAccount, flagControlPlaneServiceAccount, opts.ControlPlaneServiceAccount, "Google Service Account email for Control Plane Operator (from `hypershift infra create gcp` output)")
	flags.StringVar(&opts.CloudControllerServiceAccount, flagCloudControllerServiceAccount, opts.CloudControllerServiceAccount, "Google Service Account email for Cloud Controller Manager (from `hypershift infra create gcp` output)")
}

// ValidatedCreateOptions represents validated options for creating a GCP cluster
type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions
}

// Validate validates the GCP create cluster command options
func (o *RawCreateOptions) Validate(_ context.Context, _ *core.CreateOptions) (core.PlatformCompleter, error) {

	if err := util.ValidateRequiredOption(flagProject, o.Project); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagRegion, o.Region); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagNetwork, o.Network); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagPrivateServiceConnectSubnet, o.PrivateServiceConnectSubnet); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagWorkloadIdentityProjectNumber, o.WorkloadIdentityProjectNumber); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagWorkloadIdentityPoolID, o.WorkloadIdentityPoolID); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagWorkloadIdentityProviderID, o.WorkloadIdentityProviderID); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagNodePoolServiceAccount, o.NodePoolServiceAccount); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagControlPlaneServiceAccount, o.ControlPlaneServiceAccount); err != nil {
		return nil, err
	}
	if err := util.ValidateRequiredOption(flagCloudControllerServiceAccount, o.CloudControllerServiceAccount); err != nil {
		return nil, err
	}
	return &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: o,
		},
	}, nil
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before cluster creation can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions
}

// CreateOptions represents the completed and validated options for creating a GCP cluster
type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}

// Complete completes the GCP create cluster command options
func (o *ValidatedCreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) (core.Platform, error) {
	return &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: o,
		},
	}, nil
}

// DefaultOptions returns default options for GCP cluster creation
func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{}
}

// NewCreateCommand creates a new cobra command for creating GCP clusters
func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Creates basic functional HostedCluster resources on GCP",
		SilenceUsage: true,
	}

	gcpOpts := DefaultOptions()
	BindOptions(gcpOpts, cmd.Flags())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, gcpOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

// ApplyPlatformSpecifics applies GCP-specific configurations to the HostedCluster
func (o *CreateOptions) ApplyPlatformSpecifics(hostedCluster *hyperv1.HostedCluster) error {
	hostedCluster.Spec.Platform.Type = hyperv1.GCPPlatform
	hostedCluster.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
		Project: o.Project,
		Region:  o.Region,
		NetworkConfig: hyperv1.GCPNetworkConfig{
			Network: hyperv1.GCPResourceReference{
				Name: o.Network,
			},
			PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{
				Name: o.PrivateServiceConnectSubnet,
			},
		},
		WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: o.WorkloadIdentityProjectNumber,
			PoolID:        o.WorkloadIdentityPoolID,
			ProviderID:    o.WorkloadIdentityProviderID,
			ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
				NodePool:        o.NodePoolServiceAccount,
				ControlPlane:    o.ControlPlaneServiceAccount,
				CloudController: o.CloudControllerServiceAccount,
			},
		},
	}
	// TODO: support for external DNS will be added later after details are defined
	hostedCluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(hostedCluster.Spec.Networking.NetworkType, false)
	return nil
}

// GenerateNodePools generates the NodePool resources for GCP
func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.GCPPlatform, "")
	return []*hyperv1.NodePool{nodePool}
}

// GenerateResources generates additional resources for GCP
func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	return nil, nil
}
