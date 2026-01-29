package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewDestroyIAMCommand creates a new cobra command for destroying Azure IAM resources
// (managed identities and federated credentials) for a HostedCluster
func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Destroys Azure managed identities and federated credentials for a HostedCluster",
		SilenceUsage: true,
	}

	opts := DefaultDestroyIAMOptions()

	cmd.Flags().StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, util.WorkloadIdentitiesFileDescription)
	cmd.Flags().StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDestroyDescription)
	cmd.Flags().StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDestroyDescription)
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, util.CloudDescription)
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, util.NameDescription)
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)

	_ = cmd.MarkFlagRequired("workload-identities-file")
	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkFlagRequired("resource-group-name")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("infra-id")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			return err
		}
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to destroy IAM resources")
			return err
		}
		l.Info("Successfully destroyed IAM resources")
		return nil
	}

	return cmd
}

// DefaultDestroyIAMOptions returns DestroyIAMOptions with default values
func DefaultDestroyIAMOptions() *DestroyIAMOptions {
	return &DestroyIAMOptions{
		Cloud: "AzurePublicCloud",
	}
}

// BindDestroyIAMProductFlags binds flags for the product CLI (hcp) IAM destroy azure command
func BindDestroyIAMProductFlags(opts *DestroyIAMOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, util.WorkloadIdentitiesFileDescription)
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDestroyDescription)
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDestroyDescription)
	flags.StringVar(&opts.Cloud, "cloud", opts.Cloud, util.CloudDescription)
	flags.StringVar(&opts.Name, "name", opts.Name, util.NameDescription)
	flags.StringVar(&opts.InfraID, "infra-id", opts.InfraID, util.InfraIDDescription)
}

// Validate validates the DestroyIAMOptions
func (o *DestroyIAMOptions) Validate() error {
	if o.WorkloadIdentitiesFile == "" {
		return fmt.Errorf("workload-identities-file is required")
	}
	if o.CredentialsFile == "" && o.Credentials == nil {
		return fmt.Errorf("azure-creds is required")
	}
	if o.ResourceGroupName == "" {
		return fmt.Errorf("resource-group-name is required")
	}
	if o.Name == "" {
		return fmt.Errorf("name is required")
	}
	if o.InfraID == "" {
		return fmt.Errorf("infra-id is required")
	}
	return nil
}

// Run destroys the Azure IAM resources (managed identities and federated credentials)
func (o *DestroyIAMOptions) Run(ctx context.Context, l logr.Logger) error {
	// Read and validate the workload identities file format
	if err := o.readWorkloadIdentitiesFile(); err != nil {
		return fmt.Errorf("failed to read workload identities file: %w", err)
	}

	// Setup Azure credentials
	subscriptionID, azureCreds, err := util.SetupAzureCredentials(l, o.Credentials, o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to setup Azure credentials: %w", err)
	}

	l.Info("Deleting Azure managed identities and federated credentials",
		"clusterName", o.Name,
		"infraID", o.InfraID,
		"resourceGroup", o.ResourceGroupName)

	// Create the identity manager
	identityManager := NewIdentityManager(subscriptionID, azureCreds)

	// Destroy workload identities and federated credentials
	if err := identityManager.DestroyWorkloadIdentities(ctx, l, o.Name, o.InfraID, o.ResourceGroupName); err != nil {
		return fmt.Errorf("failed to destroy workload identities: %w", err)
	}

	l.Info("Successfully deleted all workload identities",
		"clusterName", o.Name,
		"infraID", o.InfraID,
		"resourceGroup", o.ResourceGroupName)

	return nil
}

// readWorkloadIdentitiesFile reads and parses the workload identities JSON file.
// The file format is the direct AzureWorkloadIdentities format, which is the output
// of 'hypershift create iam azure' and is directly consumable by --workload-identities-file.
func (o *DestroyIAMOptions) readWorkloadIdentitiesFile() error {
	data, err := os.ReadFile(o.WorkloadIdentitiesFile)
	if err != nil {
		return fmt.Errorf("cannot read workload identities file: %w", err)
	}

	// Parse to validate the file format - we don't use the parsed data since
	// we delete identities by name pattern based on infraID
	var workloadIdentities map[string]any
	if err := json.Unmarshal(data, &workloadIdentities); err != nil {
		return fmt.Errorf("cannot parse workload identities file: %w", err)
	}

	return nil
}
