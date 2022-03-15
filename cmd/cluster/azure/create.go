package azure

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	opts.AzurePlatform.InstanceType = "Standard_D4s_v4"
	opts.AzurePlatform.DiskSizeGB = 120
	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.InstanceType, "instance-type", opts.AzurePlatform.InstanceType, "The instance type to use for nodes")
	cmd.Flags().Int32Var(&opts.AzurePlatform.DiskSizeGB, "root-disk-size", opts.AzurePlatform.DiskSizeGB, "The size of the root disk for machines in the NodePool (minimum 16)")
	cmd.Flags().StringSliceVar(&opts.AzurePlatform.AvailabilityZones, "availablity-zones", opts.AzurePlatform.AvailabilityZones, "The availablity zones in which NodePools will be created. Must be left unspecified if the region does not support AZs. If set, one nodepool per zone will be created.")

	cmd.MarkFlagRequired("azure-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		if opts.Timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), opts.Timeout)
		}
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	if err := core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) error {
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
		infraID := infraid.New(opts.Name)
		infra, err = (&azureinfra.CreateInfraOptions{
			Name:            opts.Name,
			Location:        opts.AzurePlatform.Location,
			InfraID:         infraID,
			CredentialsFile: opts.AzurePlatform.CredentialsFile,
			BaseDomain:      opts.BaseDomain,
		}).Run(ctx)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.InfraID = infra.InfraID
	exampleOptions.Azure = &apifixtures.ExampleAzureOptions{
		Location:          infra.Location,
		ResourceGroupName: infra.ResourceGroupName,
		VnetName:          infra.VnetName,
		VnetID:            infra.VNetID,
		SubnetName:        infra.SubnetName,
		BootImageID:       infra.BootImageID,
		MachineIdentityID: infra.MachineIdentityID,
		InstanceType:      opts.AzurePlatform.InstanceType,
		SecurityGroupName: infra.SecurityGroupName,
		DiskSizeGB:        opts.AzurePlatform.DiskSizeGB,
		AvailabilityZones: opts.AzurePlatform.AvailabilityZones,
	}

	azureCredsRaw, err := ioutil.ReadFile(opts.AzurePlatform.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read --azure-creds file %s: %w", opts.AzurePlatform.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &exampleOptions.Azure.Creds); err != nil {
		return fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}
	return nil
}
