package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	powervsinfra "github.com/openshift/hypershift/cmd/infra/powervs"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/spf13/cobra"
)

const (
	defaultCIDRBlock = "10.0.0.0/16"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Creates basic functional HostedCluster resources on PowerVS PowerVS",
		SilenceUsage: true,
	}

	opts.PowerVSPlatform = core.PowerVSPlatformOptions{
		APIKey:        os.Getenv("IBMCLOUD_API_KEY"),
		PowerVSRegion: "us-south",
		PowerVSZone:   "us-south",
		VpcRegion:     "us-south",
		SysType:       "s922",
		ProcType:      "shared",
		Processors:    "0.5",
		Memory:        "32",
	}

	cmd.Flags().StringVar(&opts.PowerVSPlatform.ResourceGroup, "resource-group", "", "IBM Cloud Resource group")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.PowerVSRegion, "powervs-region", opts.PowerVSPlatform.PowerVSRegion, "IBM Cloud PowerVS region")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.PowerVSZone, "powervs-zone", opts.PowerVSPlatform.PowerVSZone, "IBM Cloud PowerVS zone")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.PowerVSCloudInstanceID, "powervs-cloud-instance-id", "", "IBM PowerVS Cloud Instance ID")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.PowerVSCloudConnection, "powervs-cloud-connection", "", "IBM Cloud PowerVS Cloud Connection")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VpcRegion, "vpc-region", opts.PowerVSPlatform.VpcRegion, "Name region")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Vpc, "vpc", "", "Name Name")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.SysType, "sys-type", opts.PowerVSPlatform.SysType, "System type used to host the instance(e.g: s922, e980, e880)")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.ProcType, "proc-type", opts.PowerVSPlatform.ProcType, "Processor type (dedicated, shared, capped)")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Processors, "processors", opts.PowerVSPlatform.Processors, "Number of processors allocated")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Memory, "memory", opts.PowerVSPlatform.Memory, "Amount of memory allocated (in GB)")

	cmd.MarkFlagRequired("resource-group")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	// for using these flags, the connection b/w all the resources should be pre-set up properly
	// e.g. cloud instance should contain a cloud connection attached to the dhcp server and provided vpc
	cmd.Flags().MarkHidden("powervs-cloud-instance-id")
	cmd.Flags().MarkHidden("powervs-cloud-connection")
	cmd.Flags().MarkHidden("vpc")

	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if opts.BaseDomain == "" {
			return fmt.Errorf("--base-domain can't be empty")
		}
		return nil
	}
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
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
	if err := validate(opts); err != nil {
		return err
	}
	if err := core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func validate(opts *core.CreateOptions) error {
	if opts.BaseDomain == "" {
		return fmt.Errorf("--base-domain can't be empty")
	}

	if opts.PowerVSPlatform.APIKey == "" {
		return fmt.Errorf("IBMCLOUD_API_KEY not set")
	}
	return nil
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = infraid.New(opts.Name)
	}

	// Load or create infrastructure for the cluster
	var infra *powervsinfra.Infra
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := ioutil.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &powervsinfra.Infra{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}

	if infra == nil {
		if len(infraID) == 0 {
			infraID = infraid.New(opts.Name)
		}
		opt := &powervsinfra.CreateInfraOptions{
			BaseDomain:             opts.BaseDomain,
			ResourceGroup:          opts.PowerVSPlatform.ResourceGroup,
			InfraID:                infraID,
			PowerVSRegion:          opts.PowerVSPlatform.PowerVSRegion,
			PowerVSZone:            opts.PowerVSPlatform.PowerVSZone,
			PowerVSCloudInstanceID: opts.PowerVSPlatform.PowerVSCloudInstanceID,
			PowerVSCloudConnection: opts.PowerVSPlatform.PowerVSCloudConnection,
			VpcRegion:              opts.PowerVSPlatform.VpcRegion,
			Vpc:                    opts.PowerVSPlatform.Vpc,
			Debug:                  true,
		}
		infra = &powervsinfra.Infra{}
		err = infra.SetupInfra(opt)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.BaseDomain = opts.BaseDomain
	exampleOptions.ComputeCIDR = defaultCIDRBlock
	exampleOptions.PrivateZoneID = infra.CisDomainID
	exampleOptions.PublicZoneID = infra.CisDomainID
	exampleOptions.InfraID = infraID
	exampleOptions.PowerVS = &apifixtures.ExamplePowerVSOptions{
		ApiKey:                 opts.PowerVSPlatform.APIKey,
		AccountID:              infra.AccountID,
		ResourceGroup:          opts.PowerVSPlatform.ResourceGroup,
		PowerVSRegion:          opts.PowerVSPlatform.PowerVSRegion,
		PowerVSZone:            opts.PowerVSPlatform.PowerVSZone,
		PowerVSCloudInstanceID: infra.PowerVSCloudInstanceID,
		PowerVSSubnetID:        infra.PowerVSDhcpSubnetID,
		VpcRegion:              opts.PowerVSPlatform.VpcRegion,
		Vpc:                    infra.VpcName,
		VpcSubnet:              infra.VpcSubnetName,
		SysType:                opts.PowerVSPlatform.SysType,
		ProcType:               opts.PowerVSPlatform.ProcType,
		Processors:             opts.PowerVSPlatform.Processors,
		Memory:                 opts.PowerVSPlatform.Memory,
	}
	return nil
}
