package powervs

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/cmd/cluster/core"
	powervsinfra "github.com/openshift/hypershift/cmd/infra/powervs"
	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Destroys a HostedCluster and its resources on PowerVS",
		SilenceUsage: true,
	}

	opts.PowerVSPlatform = core.PowerVSPlatformDestroyOptions{
		Region:                 "us-south",
		Zone:                   "us-south",
		VPCRegion:              "us-south",
		TransitGatewayLocation: "us-south",
	}

	cmd.Flags().StringVar(&opts.PowerVSPlatform.ResourceGroup, "resource-group", opts.PowerVSPlatform.ResourceGroup, "IBM Cloud Resource group")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag IBM Cloud resources")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.BaseDomain, "base-domain", opts.PowerVSPlatform.BaseDomain, "Cluster's base domain")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Region, "region", opts.PowerVSPlatform.Region, "IBM Cloud region. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.Zone, "zone", opts.PowerVSPlatform.Zone, "IBM Cloud zone. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VPCRegion, "vpc-region", opts.PowerVSPlatform.VPCRegion, "IBM Cloud VPC Region for VPC resources. Default is us-south")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.VPC, "vpc", "", "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudInstanceID, "cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID. Use this flag to reuse an existing PowerVS Service Instance resource for cluster's infra")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.CloudConnection, "cloud-connection", "", "Cloud Connection in given zone. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	cmd.Flags().BoolVar(&opts.PowerVSPlatform.Debug, "debug", opts.PowerVSPlatform.Debug, "Enabling this will print PowerVS API Request & Response logs")
	cmd.Flags().BoolVar(&opts.PowerVSPlatform.PER, "power-edge-router", opts.PowerVSPlatform.PER, "Enabling this flag ensures that the Power Edge router that was used to create the cluster is cleaned up")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.TransitGatewayLocation, "transit-gateway-location", opts.PowerVSPlatform.TransitGatewayLocation, "IBM Cloud Transit Gateway location")
	cmd.Flags().StringVar(&opts.PowerVSPlatform.TransitGateway, "transit-gateway", opts.PowerVSPlatform.TransitGateway, "IBM Cloud Transit Gateway. Use this flag to reuse an existing Transit Gateway resource for cluster's infra")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	_ = cmd.Flags().MarkHidden("cloud-instance-id")
	_ = cmd.Flags().MarkHidden("cloud-connection")
	_ = cmd.Flags().MarkHidden("vpc")
	_ = cmd.Flags().MarkHidden("transit-gateway")

	logger := log.Log
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := DestroyCluster(ctx, opts); err != nil {
			logger.Error(err, "Failed to destroy cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func DestroyCluster(ctx context.Context, o *core.DestroyOptions) error {
	hostedCluster, err := core.GetCluster(ctx, o)
	if err != nil {
		return err
	}
	if hostedCluster != nil {
		o.InfraID = hostedCluster.Spec.InfraID
		o.PowerVSPlatform.BaseDomain = hostedCluster.Spec.DNS.BaseDomain
		o.PowerVSPlatform.ResourceGroup = hostedCluster.Spec.Platform.PowerVS.ResourceGroup
		o.PowerVSPlatform.Region = hostedCluster.Spec.Platform.PowerVS.Region
		o.PowerVSPlatform.Zone = hostedCluster.Spec.Platform.PowerVS.Zone
		o.PowerVSPlatform.VPCRegion = hostedCluster.Spec.Platform.PowerVS.VPC.Region
		o.PowerVSPlatform.CISCRN = hostedCluster.Spec.Platform.PowerVS.CISInstanceCRN
		o.PowerVSPlatform.CISDomainID = hostedCluster.Spec.DNS.PrivateZoneID
	}

	var inputErrors []error
	if o.InfraID == "" {
		inputErrors = append(inputErrors, fmt.Errorf("infrastructure ID is required"))
	}
	if o.PowerVSPlatform.BaseDomain == "" {
		inputErrors = append(inputErrors, fmt.Errorf("base domain is required"))
	}
	if o.PowerVSPlatform.Region == "" {
		inputErrors = append(inputErrors, fmt.Errorf("PowerVS region is required"))
	}
	if o.PowerVSPlatform.Zone == "" {
		inputErrors = append(inputErrors, fmt.Errorf("PowerVS zone is required"))
	}
	if o.PowerVSPlatform.VPCRegion == "" {
		inputErrors = append(inputErrors, fmt.Errorf("VPC region is required"))
	}
	if o.PowerVSPlatform.ResourceGroup == "" {
		inputErrors = append(inputErrors, fmt.Errorf("resource group is required"))
	}
	if o.PowerVSPlatform.PER && o.PowerVSPlatform.TransitGatewayLocation == "" {
		inputErrors = append(inputErrors, fmt.Errorf("transit gateway location is required if use-power-edge-router flag is enabled"))
	}

	if err := errors.NewAggregate(inputErrors); err != nil {
		return fmt.Errorf("required inputs are missing: %w", err)
	}

	return core.DestroyCluster(ctx, hostedCluster, o, destroyPlatformSpecifics)
}

func destroyPlatformSpecifics(ctx context.Context, o *core.DestroyOptions) error {
	return (&powervsinfra.DestroyInfraOptions{
		Name:                   o.Name,
		Namespace:              o.Namespace,
		InfraID:                o.InfraID,
		BaseDomain:             o.PowerVSPlatform.BaseDomain,
		CISCRN:                 o.PowerVSPlatform.CISCRN,
		CISDomainID:            o.PowerVSPlatform.CISDomainID,
		ResourceGroup:          o.PowerVSPlatform.ResourceGroup,
		Region:                 o.PowerVSPlatform.Region,
		Zone:                   o.PowerVSPlatform.Zone,
		VPCRegion:              o.PowerVSPlatform.VPCRegion,
		VPC:                    o.PowerVSPlatform.VPC,
		CloudInstanceID:        o.PowerVSPlatform.CloudInstanceID,
		CloudConnection:        o.PowerVSPlatform.CloudConnection,
		Debug:                  o.PowerVSPlatform.Debug,
		PER:                    o.PowerVSPlatform.PER,
		TransitGatewayLocation: o.PowerVSPlatform.TransitGatewayLocation,
		TransitGateway:         o.PowerVSPlatform.TransitGateway,
	}).Run(ctx, o.Log)
}
