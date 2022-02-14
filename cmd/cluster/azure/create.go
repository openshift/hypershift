package azure

import (
	"context"
	"fmt"
	"io/ioutil"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/yaml"
)

type CreateOptions struct {
	CredentialsFile string
	Location        string
	InstanceType    string
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	platformOpts := CreateOptions{}

	platformOpts.Location = "eastus"
	platformOpts.InstanceType = "Standard_D4s_v4"
	cmd.Flags().StringVar(&platformOpts.CredentialsFile, "azure-creds", platformOpts.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&platformOpts.Location, "location", platformOpts.Location, "Location for the cluster")
	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "The instance type to use for nodes")

	cmd.MarkFlagRequired("azure-creds")

	cmd.RunE = opts.CreateRunFunc(&platformOpts)

	return cmd
}

func (o *CreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) error {
	var infra *azureinfra.CreateInfraOutput
	var err error
	if opts.InfrastructureJSON != "" {
		rawInfra, err := ioutil.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		if err := yaml.Unmarshal(rawInfra, &infra); err != nil {
			return fmt.Errorf("failed to deserialize infra json file: %w", err)
		}
	} else {
		infraID := fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
		infra, err = (&azureinfra.CreateInfraOptions{
			Name:            opts.Name,
			Location:        o.Location,
			InfraID:         infraID,
			CredentialsFile: o.CredentialsFile,
		}).Run(ctx)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.InfraID = infra.InfraID
	exampleOptions.Azure = &apifixtures.ExampleAzureOptions{
		Location:          infra.Location,
		ResourceGroupName: infra.ResourceGroupName,
		VnetName:          infra.VnetName,
		VnetID:            infra.VNetID,
		BootImageID:       infra.BootImageID,
		MachineIdentityID: infra.MachineIdentityID,
		InstanceType:      o.InstanceType,
		SecurityGroupName: infra.SecurityGroupName,
	}

	azureCredsRaw, err := ioutil.ReadFile(o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read --azure-creds file %s: %w", o.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &exampleOptions.Azure.Creds); err != nil {
		return fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}
	return nil
}
