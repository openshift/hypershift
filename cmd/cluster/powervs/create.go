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
		Region:     "us-south",
		Zone:       "us-south",
		VpcRegion:  "us-south",
		SysType:    "s922",
		ProcType:   "shared",
		Processors: "0.5",
		Memory:     32,
	}

	cmd.Flags().StringVar(&opts.PowerVSPlatform.ResourceGroup, "resource-group", "", "IBM Cloud Resource group")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Region, "region", opts.PowerVSPlatform.Region, "IBM Cloud region. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Zone, "zone", opts.PowerVSPlatform.Zone, "IBM Cloud zone. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudInstanceID, "cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudConnection, "cloud-connection", "", "Cloud Connection in given zone")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VpcRegion, "vpc-region", opts.PowerVSPlatform.VpcRegion, "IBM Cloud VPC Region for VPC resources. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Vpc, "vpc", "", "IBM Cloud VPC Name")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.SysType, "sys-type", opts.PowerVSPlatform.SysType, "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.ProcType, "proc-type", opts.PowerVSPlatform.ProcType, "Processor type (dedicated, shared, capped). Default is shared")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Processors, "processors", opts.PowerVSPlatform.Processors, "Number of processors allocated. Default is 0.5")
	cmd.Flags().Int32Var(&opts.PowerVSPlatform.Memory, "memory", opts.PowerVSPlatform.Memory, "Amount of memory allocated (in GB). Default is 32")

	cmd.MarkFlagRequired("resource-group")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	cmd.Flags().MarkHidden("cloud-instance-id")
	cmd.Flags().MarkHidden("cloud-connection")
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
			opts.Log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	var err error
	opts.PowerVSPlatform.APIKey, err = powervsinfra.GetAPIKey()
	if err != nil {
		return fmt.Errorf("error retrieving IBM Cloud API Key %w", err)
	}

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
		return fmt.Errorf("cloud API Key not set. Set it with IBMCLOUD_API_KEY env var or set file path containing API Key credential in IBMCLOUD_CREDENTIALS")
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
			OutputFile:             opts.InfrastructureJSON,
			PowerVSRegion:          opts.PowerVSPlatform.Region,
			PowerVSZone:            opts.PowerVSPlatform.Zone,
			PowerVSCloudInstanceID: opts.PowerVSPlatform.CloudInstanceID,
			PowerVSCloudConnection: opts.PowerVSPlatform.CloudConnection,
			VpcRegion:              opts.PowerVSPlatform.VpcRegion,
			Vpc:                    opts.PowerVSPlatform.Vpc,
		}
		infra = &powervsinfra.Infra{}
		err = infra.SetupInfra(opt)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.BaseDomain = opts.BaseDomain
	exampleOptions.MachineCIDR = defaultCIDRBlock
	exampleOptions.PrivateZoneID = infra.CisDomainID
	exampleOptions.PublicZoneID = infra.CisDomainID
	exampleOptions.InfraID = infraID
	exampleOptions.PowerVS = &apifixtures.ExamplePowerVSOptions{
		ApiKey:          opts.PowerVSPlatform.APIKey,
		AccountID:       infra.AccountID,
		ResourceGroup:   opts.PowerVSPlatform.ResourceGroup,
		Region:          opts.PowerVSPlatform.Region,
		Zone:            opts.PowerVSPlatform.Zone,
		CISInstanceCRN:  infra.CisCrn,
		CloudInstanceID: infra.PowerVSCloudInstanceID,
		Subnet:          infra.PowerVSDhcpSubnet,
		SubnetID:        infra.PowerVSDhcpSubnetID,
		VpcRegion:       opts.PowerVSPlatform.VpcRegion,
		Vpc:             infra.VpcName,
		VpcSubnet:       infra.VpcSubnetName,
		SysType:         opts.PowerVSPlatform.SysType,
		ProcType:        opts.PowerVSPlatform.ProcType,
		Processors:      opts.PowerVSPlatform.Processors,
		Memory:          opts.PowerVSPlatform.Memory,
	}
	return nil
}
