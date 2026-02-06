package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/config"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewCreateIAMCommand creates a new cobra command for creating Azure IAM resources
// (managed identities and federated credentials) for a HostedCluster
func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates Azure managed identities and federated credentials for a HostedCluster",
		SilenceUsage: true,
	}

	opts := DefaultCreateIAMOptions()

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, util.NameDescription)
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	cmd.Flags().StringVar(&opts.OIDCIssuerURL, "oidc-issuer-url", opts.OIDCIssuerURL, util.OIDCIssuerURLDescription)
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, util.WorkloadIdentitiesOutputFileDescription)
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, util.CloudDescription)

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("resource-group-name")
	_ = cmd.MarkFlagRequired("oidc-issuer-url")
	_ = cmd.MarkFlagRequired("output-file")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create IAM resources")
			return err
		}
		l.Info("Successfully created IAM resources")
		return nil
	}

	return cmd
}

// DefaultCreateIAMOptions returns CreateIAMOptions with default values
func DefaultCreateIAMOptions() *CreateIAMOptions {
	return &CreateIAMOptions{
		Location: config.DefaultAzureLocation,
		Cloud:    config.DefaultAzureCloud,
	}
}

// BindCreateIAMProductFlags binds flags for the product CLI (hcp) IAM create azure command
func BindCreateIAMProductFlags(opts *CreateIAMOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Name, "name", opts.Name, util.NameDescription)
	flags.StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)
	flags.StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	flags.StringVar(&opts.OIDCIssuerURL, "oidc-issuer-url", opts.OIDCIssuerURL, util.OIDCIssuerURLDescription)
	flags.StringVar(&opts.OutputFile, "output-file", opts.OutputFile, util.WorkloadIdentitiesOutputFileDescription)
	flags.StringVar(&opts.Cloud, "cloud", opts.Cloud, util.CloudDescription)
}

// Validate validates the CreateIAMOptions
func (o *CreateIAMOptions) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("name is required")
	}
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	if o.CredentialsFile == "" && o.Credentials == nil {
		return fmt.Errorf("azure-creds is required")
	}
	if o.ResourceGroupName == "" {
		return fmt.Errorf("resource-group-name is required")
	}
	if o.OIDCIssuerURL == "" {
		return fmt.Errorf("oidc-issuer-url is required")
	}
	if o.OutputFile == "" {
		return fmt.Errorf("output-file is required")
	}
	return nil
}

// Run creates the Azure IAM resources (managed identities and federated credentials)
func (o *CreateIAMOptions) Run(ctx context.Context, l logr.Logger) error {
	// Setup Azure credentials
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(l, o.Credentials, o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	l.Info("Creating Azure managed identities and federated credentials",
		"clusterName", o.Name,
		"infraID", o.InfraID,
		"resourceGroup", o.ResourceGroupName,
		"location", o.Location)

	// Create the identity manager
	identityManager := NewIdentityManager(subscriptionID, azureCreds, o.Cloud)

	// Create workload identities and federated credentials
	workloadIdentities, err := identityManager.CreateWorkloadIdentitiesFromIAMOptions(ctx, l, o, o.ResourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to create workload identities: %w", err)
	}

	// Write output to file (directly as AzureWorkloadIdentities for compatibility
	// with --workload-identities-file flag in create cluster/infra commands)
	if err := o.writeOutput(workloadIdentities); err != nil {
		return err
	}

	l.Info("Workload identities created and saved",
		"outputFile", o.OutputFile)

	return nil
}

// writeOutput writes the AzureWorkloadIdentities to the specified output file.
// The output format is directly consumable by --workload-identities-file in
// create cluster/infra azure commands.
func (o *CreateIAMOptions) writeOutput(workloadIdentities any) error {
	out := os.Stdout
	if o.OutputFile != "" {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func() {
			_ = out.Close()
		}()
	}

	outputBytes, err := json.MarshalIndent(workloadIdentities, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize output: %w", err)
	}

	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	return nil
}
