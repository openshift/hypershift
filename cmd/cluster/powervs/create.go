package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
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
		VPCRegion:  "us-south",
		SysType:    "s922",
		ProcType:   hyperv1.PowerVSNodePoolSharedProcType,
		Processors: "0.5",
		Memory:     32,
	}

	cmd.Flags().StringVar(&opts.PowerVSPlatform.ResourceGroup, "resource-group", "", "IBM Cloud Resource group")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Region, "region", opts.PowerVSPlatform.Region, "IBM Cloud region. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Zone, "zone", opts.PowerVSPlatform.Zone, "IBM Cloud zone. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudInstanceID, "cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudConnection, "cloud-connection", "", "Cloud Connection in given zone")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VPCRegion, "vpc-region", opts.PowerVSPlatform.VPCRegion, "IBM Cloud VPC Region for VPC resources. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VPC, "vpc", "", "IBM Cloud VPC Name")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.SysType, "sys-type", opts.PowerVSPlatform.SysType, "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	cmd.Flags().Var(&opts.PowerVSPlatform.ProcType, "proc-type", "Processor type (dedicated, shared, capped). Default is shared")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Processors, "processors", opts.PowerVSPlatform.Processors, "Number of processors allocated. Default is 0.5")
	cmd.Flags().Int32Var(&opts.PowerVSPlatform.Memory, "memory", opts.PowerVSPlatform.Memory, "Amount of memory allocated (in GB). Default is 32")
	cmd.Flags().BoolVar(&opts.PowerVSPlatform.Debug, "debug", opts.PowerVSPlatform.Debug, "Enabling this will print PowerVS API Request & Response logs")
	cmd.Flags().BoolVar(&opts.PowerVSPlatform.RecreateSecrets, "recreate-secrets", opts.PowerVSPlatform.RecreateSecrets, "Enabling this flag will recreate creds mentioned https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.PowerVSPlatformSpec here. This is required when rerunning 'hypershift create cluster powervs' or 'hypershift create infra powervs' commands, since API key once created cannot be retrieved again. Please make sure that cluster name used is unique across different management clusters before using this flag")

	cmd.MarkFlagRequired("resource-group")
	cmd.MarkPersistentFlagRequired("pull-secret")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	cmd.Flags().MarkHidden("cloud-instance-id")
	cmd.Flags().MarkHidden("cloud-connection")
	cmd.Flags().MarkHidden("vpc")

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
	if err = core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	infraID := opts.InfraID
	if len(infraID) == 0 {
		infraID = infraid.New(opts.Name)
	}

	// Load or create infrastructure for the cluster
	var infra *powervsinfra.Infra
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &powervsinfra.Infra{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}

	if opts.BaseDomain == "" && infra == nil {
		return fmt.Errorf("base-domain flag is required if infra-json is not provided")
	}

	if opts.PowerVSPlatform.ResourceGroup == "" && infra == nil {
		return fmt.Errorf("resource-group flag is required if infra-json is not provided")
	}

	if infra == nil {
		opt := &powervsinfra.CreateInfraOptions{
			Name:            opts.Name,
			Namespace:       opts.Namespace,
			BaseDomain:      opts.BaseDomain,
			ResourceGroup:   opts.PowerVSPlatform.ResourceGroup,
			InfraID:         infraID,
			OutputFile:      opts.InfrastructureJSON,
			Region:          opts.PowerVSPlatform.Region,
			Zone:            opts.PowerVSPlatform.Zone,
			CloudInstanceID: opts.PowerVSPlatform.CloudInstanceID,
			CloudConnection: opts.PowerVSPlatform.CloudConnection,
			VPCRegion:       opts.PowerVSPlatform.VPCRegion,
			VPC:             opts.PowerVSPlatform.VPC,
			Debug:           opts.PowerVSPlatform.Debug,
			RecreateSecrets: opts.PowerVSPlatform.RecreateSecrets,
		}
		infra = &powervsinfra.Infra{
			ID:            infraID,
			BaseDomain:    opts.BaseDomain,
			ResourceGroup: opts.PowerVSPlatform.ResourceGroup,
			Region:        opts.PowerVSPlatform.Region,
			Zone:          opts.PowerVSPlatform.Zone,
			VPCRegion:     opts.PowerVSPlatform.VPCRegion,
		}
		err = infra.SetupInfra(ctx, opt)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.MachineCIDR = defaultCIDRBlock
	exampleOptions.PrivateZoneID = infra.CISDomainID
	exampleOptions.PublicZoneID = infra.CISDomainID
	exampleOptions.InfraID = infra.ID
	exampleOptions.PowerVS = &apifixtures.ExamplePowerVSOptions{
		AccountID:       infra.AccountID,
		ResourceGroup:   infra.ResourceGroup,
		Region:          infra.Region,
		Zone:            infra.Zone,
		CISInstanceCRN:  infra.CISCRN,
		CloudInstanceID: infra.CloudInstanceID,
		Subnet:          infra.DHCPSubnet,
		SubnetID:        infra.DHCPSubnetID,
		VPCRegion:       infra.VPCRegion,
		VPC:             infra.VPCName,
		VPCSubnet:       infra.VPCSubnetName,
		SysType:         opts.PowerVSPlatform.SysType,
		ProcType:        opts.PowerVSPlatform.ProcType,
		Processors:      opts.PowerVSPlatform.Processors,
		Memory:          opts.PowerVSPlatform.Memory,
	}

	powerVSResources := apifixtures.ExamplePowerVSResources{
		KubeCloudControllerCreds:  infra.Secrets.KubeCloudControllerManager,
		NodePoolManagementCreds:   infra.Secrets.NodePoolManagement,
		IngressOperatorCloudCreds: infra.Secrets.IngressOperator,
		StorageOperatorCloudCreds: infra.Secrets.StorageOperator,
	}

	exampleOptions.PowerVS.Resources = powerVSResources

	return nil
}
