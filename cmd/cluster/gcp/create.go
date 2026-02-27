package gcp

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var _ core.Platform = (*CreateOptions)(nil)

const (
	SATokenIssuerSecret   = "sa-token-issuer-key"
	defaultGCPMachineType = "n2-standard-4"

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
	flagStorageServiceAccount         = "storage-service-account"
	flagServiceAccountSigningKeyPath  = "service-account-signing-key-path"
	flagEndpointAccess                = "endpoint-access"
	flagIssuerURL                     = "oidc-issuer-url"
	flagMachineType                   = "machine-type"
	flagZone                          = "zone"
	flagSubnet                        = "subnet"
	flagBootImage                     = "boot-image"
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

	// StorageServiceAccount is the Google Service Account email for the GCP PD CSI Driver
	StorageServiceAccount string

	// ServiceAccountSigningKeyPath is the path to the private key file for the service account token issuer
	ServiceAccountSigningKeyPath string

	// EndpointAccess controls API endpoint accessibility (Private or PublicAndPrivate)
	EndpointAccess string

	// IssuerURL is the OIDC provider issuer URL
	IssuerURL string

	// MachineType is the GCP machine type for node instances (e.g. n2-standard-4)
	MachineType string

	// Zone is the GCP zone for node instances (e.g. us-central1-a)
	Zone string

	// Subnet is the subnet name for node instances
	Subnet string

	// BootImage is the GCP boot image for node instances. Overrides the default RHCOS image from the release payload
	BootImage string
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
	flags.StringVar(&opts.NodePoolServiceAccount, flagNodePoolServiceAccount, opts.NodePoolServiceAccount, "Google Service Account email for NodePool CAPG controllers (from `hypershift create iam gcp` output)")
	flags.StringVar(&opts.ControlPlaneServiceAccount, flagControlPlaneServiceAccount, opts.ControlPlaneServiceAccount, "Google Service Account email for Control Plane Operator (from `hypershift create iam gcp` output)")
	flags.StringVar(&opts.CloudControllerServiceAccount, flagCloudControllerServiceAccount, opts.CloudControllerServiceAccount, "Google Service Account email for Cloud Controller Manager (from `hypershift create iam gcp` output)")
	flags.StringVar(&opts.StorageServiceAccount, flagStorageServiceAccount, opts.StorageServiceAccount, "Google Service Account email for GCP PD CSI Driver (from `hypershift create iam gcp` output)")
	flags.StringVar(&opts.ServiceAccountSigningKeyPath, flagServiceAccountSigningKeyPath, "", "The file to the private key for the service account token issuer")
	flags.StringVar(&opts.EndpointAccess, flagEndpointAccess, string(hyperv1.GCPEndpointAccessPrivate), "Endpoint access type (Private or PublicAndPrivate)")
	flags.StringVar(&opts.IssuerURL, flagIssuerURL, "", "The OIDC provider issuer URL")
	flags.StringVar(&opts.MachineType, flagMachineType, "", "GCP machine type for node instances. Defaults to "+defaultGCPMachineType)
	flags.StringVar(&opts.Zone, flagZone, "", "GCP zone for node instances (e.g. us-central1-a). Defaults to {region}-a")
	flags.StringVar(&opts.Subnet, flagSubnet, "", "Subnet name for node instances. Defaults to the PSC subnet value")
	flags.StringVar(&opts.BootImage, flagBootImage, "", "GCP boot image for node instances. Overrides the default RHCOS image from the release payload")
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
	if err := util.ValidateRequiredOption(flagStorageServiceAccount, o.StorageServiceAccount); err != nil {
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

	namespace         string
	name              string
	externalDNSDomain string
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
			namespace:              opts.Namespace,
			name:                   opts.Name,
			externalDNSDomain:      opts.ExternalDNSDomain,
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

func serviceAccountTokenIssuerSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", name, SATokenIssuerSecret),
			Namespace: namespace,
		},
	}
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
				Storage:         o.StorageServiceAccount,
			},
		},
		EndpointAccess: hyperv1.GCPEndpointAccessType(o.EndpointAccess),
	}

	hostedCluster.Spec.IssuerURL = o.IssuerURL

	if len(o.ServiceAccountSigningKeyPath) > 0 {
		hostedCluster.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{
			Name: serviceAccountTokenIssuerSecret(o.namespace, o.name).Name,
		}
	}

	hostedCluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(hostedCluster.Spec.Networking.NetworkType, o.externalDNSDomain != "")

	if o.externalDNSDomain != "" {
		// Only APIServer and OAuthServer need external DNS routes.
		// Konnectivity and Ignition are accessed via Private Service Connect (.hypershift.local).
		for i, svc := range hostedCluster.Spec.Services {
			switch svc.Service {
			case hyperv1.APIServer:
				hostedCluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("api-%s.%s", hostedCluster.Name, o.externalDNSDomain),
				}
			case hyperv1.OAuthServer:
				hostedCluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("oauth-%s.%s", hostedCluster.Name, o.externalDNSDomain),
				}
			}
		}
	}

	return nil
}

// GenerateNodePools generates the NodePool resources for GCP
func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.GCPPlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}

	machineType := o.MachineType
	if machineType == "" {
		machineType = defaultGCPMachineType
	}
	zone := o.Zone
	if zone == "" {
		zone = o.Region + "-a"
	}
	subnet := o.Subnet
	if subnet == "" {
		subnet = o.PrivateServiceConnectSubnet
	}
	var bootImage *string
	if o.BootImage != "" {
		bootImage = &o.BootImage
	}

	nodePool.Spec.Platform.GCP = &hyperv1.GCPNodePoolPlatform{
		MachineType: machineType,
		Zone:        zone,
		Subnet:      subnet,
		Image:       bootImage,
	}
	return []*hyperv1.NodePool{nodePool}
}

// GenerateResources generates additional resources for GCP
func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	var result []client.Object
	if len(o.ServiceAccountSigningKeyPath) > 0 {
		privateKey, err := os.ReadFile(o.ServiceAccountSigningKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read service account signing key file: %w", err)
		}

		saSecret := serviceAccountTokenIssuerSecret(o.namespace, o.name)
		saSecret.Data = map[string][]byte{
			"key": privateKey,
		}
		result = append(result, saSecret)
	}
	return result, nil
}
